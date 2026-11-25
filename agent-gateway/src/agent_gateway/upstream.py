"""HTTP client for the backing services, restricted to an allowlist of endpoints.

The gateway holds no trading logic (ADR-002). It can only reach the endpoints in
``ALLOWED_ENDPOINTS``; anything else is refused before a request is made. Every entry is
a read, except computing a signal and *proposing* an order intent (BL-26). Approve,
reject, cancel and kill-switch routes are deliberately absent. Upstream errors are mapped
to short, sanitized messages: raw broker errors never reach the agent.
"""

from __future__ import annotations

import contextvars
import re
from collections.abc import Mapping
from dataclasses import dataclass
from typing import Any, Literal

import httpx

from .config import GatewayConfig

Service = Literal["signal", "engine"]

ORDER_INTENTS_PATH = "/api/v1/order-intents"
ORDER_INTENT_PATH = "/api/v1/order-intents/{id}"

# Paths are templates: ``{name}`` segments are filled from ``path_params``.
ALLOWED_ENDPOINTS: frozenset[tuple[Service, str, str]] = frozenset(
    {
        ("signal", "GET", "/api/v1/signals/strategies"),
        ("signal", "POST", "/api/v1/signals/generate"),
        ("engine", "GET", "/api/v1/portfolio/equity"),
        ("engine", "GET", "/api/v1/portfolio/positions"),
        ("engine", "GET", "/api/v1/trading-mode"),
        ("engine", "GET", ORDER_INTENTS_PATH),
        ("engine", "GET", ORDER_INTENT_PATH),
        # Proposes an intent (scope intent:create). It never executes: a human approves.
        ("engine", "POST", ORDER_INTENTS_PATH),
    }
)

_SERVICE_NAMES: dict[Service, str] = {"signal": "signal-service", "engine": "bot-engine"}
_PATH_PARAM_RE = re.compile(r"^[A-Za-z0-9-]{1,64}$")
_PLACEHOLDER_RE = re.compile(r"\{([a-z_]+)\}")
_CODE_RE = re.compile(r"^[a-z][a-z0-9_]{0,63}$")
_REQUEST_ID_RE = re.compile(r"^[A-Za-z0-9._-]{1,64}$")
_SCOPE_RE = re.compile(r"^[a-z]+:[a-z*-]+$")
_UUID_IN_TEXT_RE = re.compile(r"[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}")

# Fixed, safe messages for the engine's error codes (docs/runbooks/ORDER_INTENTS.md). The
# engine's own `message`/`detail` text is never forwarded for these.
_ENGINE_MESSAGES: dict[str, str] = {
    "kill_switch_engaged": "the kill switch is engaged; no order intent was created",
    "kill_switch_unavailable": "the kill-switch state could not be read; trading stays refused",
    "intent_exists": "an active order intent already exists for this signal",
    "idempotency_key_reused": (
        "this idempotency_key was already used with a different symbol or strategy; "
        "use a new key for a different request"
    ),
    "idempotency_key_required": "the idempotency key is missing or malformed",
    "rate_limited": "this agent created too many order intents recently; retry later",
    "signal_unavailable": "signal-service did not return a signal; no order intent was created",
    "insufficient_scope": "the gateway token lacks permission",
    "not_intent_owner": "the intent belongs to another actor",
    "execution_disabled": "order execution is disabled on bot-engine",
    "audit_unavailable": "bot-engine could not write its audit record; nothing was created",
    "invalid_request": "bot-engine rejected the request as invalid",
}

# Correlation ID of the tool call in progress (BL-20); sent as X-Request-ID.
current_request_id: contextvars.ContextVar[str] = contextvars.ContextVar("request_id", default="")


@dataclass
class UpstreamError(Exception):
    """A sanitized upstream failure. ``message`` is safe to show to the agent.

    ``upstream_code`` is the service's stable ``error`` code (validated shape) and
    ``request_id`` the correlation ID, so a failure can be found in the service logs.
    """

    code: str
    message: str
    status: int | None = None
    upstream_code: str = ""
    request_id: str = ""

    def __str__(self) -> str:
        extra = []
        if self.upstream_code:
            extra.append(f"error={self.upstream_code}")
        if self.request_id:
            extra.append(f"request_id={self.request_id}")
        suffix = f" ({', '.join(extra)})" if extra else ""
        return f"{self.code}: {self.message}{suffix}"


@dataclass(frozen=True)
class UpstreamResponse:
    status: int
    headers: httpx.Headers
    body: Any


class Upstream:
    def __init__(self, config: GatewayConfig, transport: httpx.AsyncBaseTransport | None = None) -> None:
        self._base: dict[Service, str] = {
            "signal": config.signal_service_url,
            "engine": config.bot_engine_url,
        }
        self._client = httpx.AsyncClient(
            timeout=config.request_timeout_seconds,
            headers={
                "Authorization": f"Bearer {config.gateway_token}",
                "User-Agent": "signal-trading-agent-gateway",
            },
            transport=transport,
            follow_redirects=False,
        )

    async def aclose(self) -> None:
        await self._client.aclose()

    async def request(
        self,
        service: Service,
        method: str,
        path: str,
        json: dict[str, Any] | None = None,
        *,
        path_params: Mapping[str, str] | None = None,
        params: Mapping[str, str | int] | None = None,
        headers: Mapping[str, str] | None = None,
    ) -> Any:
        """Call an allowlisted endpoint and return its JSON body."""
        resp = await self.send(
            service, method, path, json, path_params=path_params, params=params, headers=headers
        )
        return resp.body

    async def send(
        self,
        service: Service,
        method: str,
        path: str,
        json: dict[str, Any] | None = None,
        *,
        path_params: Mapping[str, str] | None = None,
        params: Mapping[str, str | int] | None = None,
        headers: Mapping[str, str] | None = None,
    ) -> UpstreamResponse:
        """Like ``request`` but also returns the status and response headers."""
        if (service, method, path) not in ALLOWED_ENDPOINTS:
            raise UpstreamError("endpoint_not_allowed", f"{method} {path} is not an allowed endpoint")
        url_path = _fill_path(path, path_params or {})
        name = _SERVICE_NAMES[service]
        rid = current_request_id.get()
        send_headers = dict(headers or {})
        if rid:
            send_headers["X-Request-ID"] = rid
        try:
            resp = await self._client.request(
                method,
                self._base[service] + url_path,
                json=json,
                params=dict(params) if params else None,
                headers=send_headers or None,
            )
        except httpx.TimeoutException:
            raise UpstreamError(
                "upstream_timeout", f"{name} did not answer in time", request_id=rid
            ) from None
        except httpx.HTTPError:
            raise UpstreamError(
                "upstream_unreachable", f"{name} is not reachable", request_id=rid
            ) from None

        if resp.status_code in (200, 201):
            try:
                body = resp.json()
            except ValueError:
                raise UpstreamError(
                    "upstream_bad_response", f"{name} returned invalid JSON", resp.status_code,
                    request_id=rid,
                ) from None
            return UpstreamResponse(resp.status_code, resp.headers, body)
        raise _map_error(service, resp, rid)


def _fill_path(template: str, path_params: Mapping[str, str]) -> str:
    """Fill ``{name}`` segments; values must be plain IDs so no other route can be reached."""
    names = _PLACEHOLDER_RE.findall(template)
    if set(names) != set(path_params):
        raise UpstreamError("endpoint_not_allowed", f"{template} needs path parameters {names}")
    out = template
    for key in names:
        value = path_params[key]
        if not _PATH_PARAM_RE.match(value):
            raise UpstreamError("endpoint_not_allowed", f"invalid path parameter {key}")
        out = out.replace("{" + key + "}", value)
    return out


def _map_error(service: Service, resp: httpx.Response, sent_request_id: str = "") -> UpstreamError:
    name = _SERVICE_NAMES[service]
    status = resp.status_code
    body = _json_body(resp)
    raw_code = body.get("error")
    code = raw_code if isinstance(raw_code, str) and _CODE_RE.match(raw_code) else ""
    rid = _request_id(body, resp, sent_request_id)

    def err(gw_code: str, message: str) -> UpstreamError:
        return UpstreamError(gw_code, message, status, upstream_code=code, request_id=rid)

    known = _ENGINE_MESSAGES.get(code, "")
    if status == 401:
        return err("gateway_unauthorized", f"{name} rejected the gateway token (check GATEWAY_TOKEN)")
    if status == 403:
        scope = _detail_field(resp, "required_scope")
        suffix = (
            f" (requires {scope})" if isinstance(scope, str) and _SCOPE_RE.match(scope) else ""
        )
        if code and code != "insufficient_scope" and known:
            return err("forbidden", known)
        return err("forbidden", f"the gateway token lacks permission{suffix}")
    if status == 400:
        return err("invalid_input", known or f"{name} rejected the request as invalid")
    if status == 404:
        return err("not_found", f"{name} has no such resource")
    if status == 409:
        msg = known or f"{name} refused the request because of a conflicting state"
        if code == "intent_exists":
            existing = _UUID_IN_TEXT_RE.search(str(body.get("message") or body.get("detail") or ""))
            if existing:
                msg += f" (existing intent id: {existing.group(0)})"
        return err("conflict", msg)
    if status == 422:
        if service == "engine":
            return err("invalid_input", known or f"{name} rejected the request as invalid")
        detail = _detail_field(resp, None)
        # signal-service validation messages are safe, short strings; pydantic lists are summarized.
        msg = detail if isinstance(detail, str) and len(detail) <= 300 else "invalid input"
        return err("invalid_input", msg)
    if status == 429:
        return err("upstream_rate_limited", known or f"{name} is rate limiting requests")
    if status == 502:
        if code == "signal_unavailable":
            return err("signal_unavailable", known)
        return err("broker_unavailable", f"{name} could not reach the broker")
    if status == 503:
        if service == "engine" and known:
            return err("service_unavailable", known)
        return err("market_data_unavailable", "market data is temporarily unavailable")
    return err("upstream_error", f"{name} returned HTTP {status}")


def _json_body(resp: httpx.Response) -> dict[str, Any]:
    try:
        body = resp.json()
    except ValueError:
        return {}
    return body if isinstance(body, dict) else {}


def _request_id(body: dict[str, Any], resp: httpx.Response, sent: str) -> str:
    for candidate in (body.get("request_id"), resp.headers.get("x-request-id"), sent):
        if isinstance(candidate, str) and _REQUEST_ID_RE.match(candidate):
            return candidate
    return ""


def _detail_field(resp: httpx.Response, key: str | None) -> Any:
    try:
        body = resp.json()
    except ValueError:
        return None
    if not isinstance(body, dict):
        return None
    if key is None:
        return body.get("detail")
    if key in body:
        return body[key]
    detail = body.get("detail")
    return detail.get(key) if isinstance(detail, dict) else None
