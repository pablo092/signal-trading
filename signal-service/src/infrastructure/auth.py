"""Scoped static service tokens (ADR-003, M1) — same table format and rules as bot-engine.

The token table is a JSON array of ``{"id", "type", "sha256", "scopes"}`` entries. Only
SHA-256 digests are stored; requests send ``Authorization: Bearer <token>``.
Deny by default: with an empty table every protected route returns 401.
"""

from __future__ import annotations

import hashlib
import hmac
import json
from collections.abc import Callable
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

import structlog
from fastapi import Request, status

from src.api.errors import ApiError, request_id_of

logger = structlog.get_logger(__name__)

SCOPE_MARKET_READ = "market:read"
SCOPE_PORTFOLIO_READ = "portfolio:read"
SCOPE_INTENT_CREATE = "intent:create"
SCOPE_INTENT_APPROVE = "intent:approve"
SCOPE_ADMIN_KILLSWITCH = "admin:killswitch"
SCOPE_ORDERS_PLACE_LEGACY = "orders:place-legacy"

KNOWN_SCOPES = frozenset(
    {
        SCOPE_MARKET_READ,
        SCOPE_PORTFOLIO_READ,
        SCOPE_INTENT_CREATE,
        SCOPE_INTENT_APPROVE,
        SCOPE_ADMIN_KILLSWITCH,
        SCOPE_ORDERS_PLACE_LEGACY,
    }
)
HUMAN_ONLY_SCOPES = frozenset(
    {SCOPE_INTENT_APPROVE, SCOPE_ADMIN_KILLSWITCH, SCOPE_ORDERS_PLACE_LEGACY}
)
ACTOR_TYPES = frozenset({"agent", "human", "service"})


class TokenTableError(ValueError):
    """Invalid token table. Messages name the entry id, never its content."""


def hash_token(token: str) -> str:
    return hashlib.sha256(token.encode("utf-8")).hexdigest()


@dataclass(frozen=True)
class Principal:
    id: str
    type: str
    scopes: frozenset[str]
    digest: bytes = field(repr=False)

    def has_scope(self, scope: str) -> bool:
        return scope in self.scopes


class TokenStore:
    """Immutable, validated token table."""

    def __init__(self, principals: list[Principal]) -> None:
        self._principals = principals

    def __len__(self) -> int:
        return len(self._principals)

    @classmethod
    def from_entries(cls, entries: Any) -> TokenStore:
        if not isinstance(entries, list):
            raise TokenTableError("token table must be a JSON array")
        principals: list[Principal] = []
        seen_ids: set[str] = set()
        seen_digests: set[bytes] = set()
        for i, e in enumerate(entries):
            if not isinstance(e, dict) or not isinstance(e.get("id"), str) or not e["id"]:
                raise TokenTableError(f"token entry {i}: id is required")
            pid = e["id"]
            if pid in seen_ids:
                raise TokenTableError(f"token entry {pid!r}: duplicate id")
            seen_ids.add(pid)
            ptype = e.get("type")
            if ptype not in ACTOR_TYPES:
                raise TokenTableError(f"token entry {pid!r}: type must be agent, human or service")
            try:
                digest = bytes.fromhex(str(e.get("sha256", "")))
            except ValueError:
                digest = b""
            if len(digest) != 32:
                raise TokenTableError(
                    f"token entry {pid!r}: sha256 must be a 64-char hex digest "
                    "(never a plaintext token)"
                )
            if digest in seen_digests:
                raise TokenTableError(f"token entry {pid!r}: duplicate token hash")
            seen_digests.add(digest)
            scopes = e.get("scopes")
            if not isinstance(scopes, list) or not scopes:
                raise TokenTableError(f"token entry {pid!r}: at least one scope is required")
            for s in scopes:
                if s not in KNOWN_SCOPES:
                    raise TokenTableError(f"token entry {pid!r}: unknown scope {s!r}")
                if ptype == "agent" and s in HUMAN_ONLY_SCOPES:
                    raise TokenTableError(
                        f"token entry {pid!r}: agent tokens cannot hold scope {s!r}"
                    )
            principals.append(Principal(id=pid, type=ptype, scopes=frozenset(scopes), digest=digest))
        return cls(principals)

    @classmethod
    def load(cls, inline_json: str = "", file_path: str = "") -> TokenStore:
        """Load from inline JSON or a file (inline wins). Both empty → empty store."""
        raw = inline_json.strip()
        if not raw and file_path:
            try:
                raw = Path(file_path).read_text(encoding="utf-8").strip()
            except OSError as exc:
                raise TokenTableError("cannot read AUTH_TOKENS_FILE") from exc
        if not raw:
            return cls([])
        try:
            entries = json.loads(raw)
        except json.JSONDecodeError:
            raise TokenTableError("token table is not valid JSON") from None
        return cls.from_entries(entries)

    def authenticate(self, token: str) -> Principal | None:
        """Constant-time lookup; every entry is compared."""
        if not token:
            return None
        digest = hashlib.sha256(token.encode("utf-8")).digest()
        found: Principal | None = None
        for p in self._principals:
            if hmac.compare_digest(digest, p.digest):
                found = p
        # Defense in depth: never accept an agent principal holding a human-only scope.
        if found is not None and found.type == "agent" and found.scopes & HUMAN_ONLY_SCOPES:
            return None
        return found


def bearer_token(request: Request) -> str:
    header = request.headers.get("authorization", "")
    prefix = "bearer "
    if len(header) > len(prefix) and header[: len(prefix)].lower() == prefix:
        return header[len(prefix) :].strip()
    return ""


def require_scope(scope: str) -> Callable[[Request], Principal]:
    """FastAPI dependency: 401 without a valid token, 403 without ``scope``.

    The returned callable carries ``required_scope`` so the route-scope test can verify
    every non-public route declares one.
    """

    def dependency(request: Request) -> Principal:
        store: TokenStore = request.app.state.tokens
        principal = store.authenticate(bearer_token(request))
        route = request.scope.get("route")
        path = getattr(route, "path", request.url.path)
        if principal is None:
            _audit(request, None, scope, path, "unauthenticated")
            raise ApiError(
                status.HTTP_401_UNAUTHORIZED,
                "unauthorized",
                "missing or invalid bearer token",
                headers={"WWW-Authenticate": "Bearer"},
            )
        if not principal.has_scope(scope):
            _audit(request, principal, scope, path, "forbidden")
            raise ApiError(
                status.HTTP_403_FORBIDDEN,
                "insufficient_scope",
                "the token lacks the scope this route requires",
                required_scope=scope,
            )
        _audit(request, principal, scope, path, "allowed")
        return principal

    dependency.required_scope = scope  # type: ignore[attr-defined]
    return dependency


def _audit(request: Request, p: Principal | None, scope: str, route: str, outcome: str) -> None:
    logger.info(
        "audit.request",
        actor_id=p.id if p else "",
        actor_type=p.type if p else "",
        method=request.method,
        route=route,
        scope=scope,
        outcome=outcome,
        request_id=request_id_of(request),
    )
