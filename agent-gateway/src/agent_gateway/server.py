"""MCP server exposing market-analysis tools and order-intent proposals (ADR-002, BL-26).

Tools map 1:1 to endpoints of signal-service and bot-engine. The only write is
``create_order_intent``, which *proposes* an order: a human must approve it in bot-engine
before anything is submitted (ADR-005). There is no approve, reject, cancel, kill-switch
or direct order capability, and the gateway's service token holds only read scopes plus
``intent:create`` (never ``intent:approve``, ADR-003), so bot-engine refuses it on every
other write route even if this code were changed.
"""

from __future__ import annotations

import re
import time
import uuid
from collections.abc import AsyncIterator, Awaitable, Callable
from contextlib import asynccontextmanager
from typing import Annotated, Any, Literal, get_args

from mcp.server.mcpserver import MCPServer
from mcp.server.mcpserver.exceptions import ToolError
from mcp.types import ToolAnnotations
from pydantic import Field

from .audit import AuditLog
from .config import GatewayConfig
from .ratelimit import RateLimiter
from .upstream import (
    ORDER_INTENT_PATH,
    ORDER_INTENTS_PATH,
    Upstream,
    UpstreamError,
    current_request_id,
)

INSTRUCTIONS = (
    "Market analysis for a PAPER-TRADING account. These tools can list strategies, compute "
    "RSI / moving-average-cross signals on recent bars, read the paper portfolio and the "
    "verified trading mode, and PROPOSE order intents. A proposal executes nothing: the "
    "engine sizes it and runs risk checks, then a human must approve it before any paper "
    "order is submitted. These tools cannot approve, reject or cancel intents, nor place, "
    "modify or cancel orders. Signals are algorithmic indicators, not financial advice: "
    "always present them with that caveat."
)
DISCLAIMER = "Algorithmic indicator on a paper-trading account; not financial advice."
INTENT_NOTE = (
    "Proposal only: nothing was executed. A human must approve this intent in bot-engine "
    "before any paper order is submitted; it expires at expires_at if not approved."
)

Timeframe = Literal["1Min", "5Min", "15Min", "30Min", "1Hour", "4Hour", "1Day"]
# Must equal the status enum of contracts/schemas/order-intent.schema.json (contract test).
IntentStatus = Literal[
    "pending_risk", "pending_approval", "approved", "submitted",
    "filled", "rejected", "canceled", "expired",
]
_SYMBOL_RE = re.compile(r"^[A-Z]{1,10}$")
_STRATEGY_RE = re.compile(r"^[a-z][a-z0-9_]{0,31}$")
_IDEMPOTENCY_KEY_RE = re.compile(r"^[A-Za-z0-9._:-]{8,128}$")
_UUID_RE = re.compile(r"^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$")
_INTENT_STATUSES = frozenset(get_args(IntentStatus))
# Top-level fields of order-intent.schema.json; anything else from upstream is dropped.
INTENT_FIELDS = (
    "id", "status", "symbol", "strategy", "side", "qty", "estimated_notional", "signal",
    "created_by", "created_at", "updated_at", "expires_at", "decided_by", "decided_at",
    "decision_reason", "rejection_code", "risk", "order",
)

READ_ONLY = ToolAnnotations(
    read_only_hint=True, destructive_hint=False, idempotent_hint=True, open_world_hint=True
)
# Creates a record (a proposal) but never executes or deletes anything; a retry with the
# same idempotency key returns the original intent.
PROPOSE = ToolAnnotations(
    read_only_hint=False, destructive_hint=False, idempotent_hint=True, open_world_hint=True
)


def new_request_id(agent_id: str) -> str:
    """Per-call correlation ID, <= 64 chars so the services accept it as-is."""
    return f"{agent_id[:47]}-{uuid.uuid4().hex[:16]}"


class InputError(ValueError):
    pass


def _symbol(raw: str) -> str:
    s = raw.strip().upper()
    if not _SYMBOL_RE.match(s):
        raise InputError("symbol must be 1-10 letters, e.g. AAPL")
    return s


def _strategy(raw: str) -> str:
    s = raw.strip().lower()
    if not _STRATEGY_RE.match(s):
        raise InputError("strategy must be a strategy name such as 'rsi' or 'ma_cross'")
    return s


def _idempotency_key(raw: str) -> str:
    if not _IDEMPOTENCY_KEY_RE.match(raw):
        raise InputError("idempotency_key must be 8-128 characters of A-Z a-z 0-9 . _ : -")
    return raw


def _intent_id(raw: str) -> str:
    s = raw.strip().lower()
    if not _UUID_RE.match(s):
        raise InputError("intent_id must be a UUID such as 7d0c1b2a-3e4f-4a5b-8c6d-7e8f9a0b1c2d")
    return s


def _intent_status(raw: str | None) -> str | None:
    if raw is not None and raw not in _INTENT_STATUSES:
        raise InputError(f"status must be one of {', '.join(sorted(_INTENT_STATUSES))}")
    return raw


def _shape_intent(body: Any) -> dict[str, Any]:
    if not isinstance(body, dict) or not isinstance(body.get("id"), str):
        raise UpstreamError("upstream_bad_response", "bot-engine returned an unexpected intent body")
    return {k: body.get(k) for k in INTENT_FIELDS}


class Gateway:
    def __init__(
        self,
        config: GatewayConfig,
        upstream: Upstream | None = None,
        audit: AuditLog | None = None,
        limiter: RateLimiter | None = None,
    ) -> None:
        self.config = config
        self.upstream = upstream or Upstream(config)
        self.audit = audit or AuditLog(config.agent_id, config.audit_log_file)
        self.limiter = limiter or RateLimiter(config.rate_limit_per_minute)

    async def run(
        self, tool: str, args: dict[str, Any], fn: Callable[[], Awaitable[dict[str, Any]]]
    ) -> dict[str, Any]:
        t0 = time.perf_counter()
        request_id = new_request_id(self.config.agent_id)
        token = current_request_id.set(request_id)

        def done(outcome: str, code: str = "") -> None:
            self.audit.record(
                tool, args, outcome, (time.perf_counter() - t0) * 1000, code, request_id
            )

        try:
            return await self._run(tool, fn, done)
        finally:
            current_request_id.reset(token)

    async def _run(
        self,
        tool: str,
        fn: Callable[[], Awaitable[dict[str, Any]]],
        done: Callable[..., None],
    ) -> dict[str, Any]:
        if not self.limiter.allow(self.config.agent_id):
            wait = self.limiter.retry_after(self.config.agent_id)
            done("rate_limited", "rate_limited")
            raise ToolError(f"rate_limited: too many tool calls, retry in {wait:.0f}s")
        try:
            result = await fn()
        except InputError as exc:
            done("error", "invalid_input")
            raise ToolError(f"invalid_input: {exc}") from None
        except UpstreamError as exc:
            done("error", exc.code)
            raise ToolError(str(exc)) from None
        except Exception:
            done("error", "internal_error")
            raise ToolError("internal_error: the gateway failed to process the request") from None
        done("ok")
        return result


def build_server(gateway: Gateway) -> MCPServer:
    @asynccontextmanager
    async def lifespan(_: MCPServer[Any]) -> AsyncIterator[None]:
        try:
            yield
        finally:
            await gateway.upstream.aclose()

    mcp: MCPServer[Any] = MCPServer(
        name="signal-trading",
        title="Signal Trading (paper; analysis and order proposals)",
        instructions=INSTRUCTIONS,
        version="0.1.0",
        lifespan=lifespan,
    )
    up = gateway.upstream

    @mcp.tool(
        title="List strategies",
        description="List the signal strategies available for generate_signal.",
        annotations=READ_ONLY,
    )
    async def list_strategies() -> dict[str, Any]:
        async def call() -> dict[str, Any]:
            body = await up.request("signal", "GET", "/api/v1/signals/strategies")
            return {"strategies": list(body.get("strategies", []))}

        return await gateway.run("list_strategies", {}, call)

    @mcp.tool(
        title="Generate signal",
        description=(
            "Compute a BUY/SELL/HOLD signal for a US stock from recent OHLCV bars using a "
            "strategy (see list_strategies). Read-only: places no order. " + DISCLAIMER
        ),
        annotations=READ_ONLY,
    )
    async def generate_signal(
        symbol: Annotated[str, Field(description="Ticker, 1-10 letters, e.g. AAPL")],
        strategy: Annotated[str, Field(description="Strategy name, e.g. rsi or ma_cross")] = "rsi",
        timeframe: Annotated[Timeframe, Field(description="Bar size")] = "1Hour",
        bars: Annotated[int, Field(ge=20, le=1000, description="Bars to analyze (20-1000)")] = 100,
    ) -> dict[str, Any]:
        args = {"symbol": symbol, "strategy": strategy, "timeframe": timeframe, "bars": bars}

        async def call() -> dict[str, Any]:
            payload = {
                "symbol": _symbol(symbol),
                "strategy": _strategy(strategy),
                "timeframe": timeframe,
                "bars_limit": bars,
            }
            body = await up.request("signal", "POST", "/api/v1/signals/generate", json=payload)
            keys = (
                "symbol", "direction", "strength", "confidence", "price", "reason",
                "strategy_name", "is_actionable", "generated_at",
            )
            out = {k: body.get(k) for k in keys}
            out["timeframe"] = timeframe
            out["disclaimer"] = DISCLAIMER
            return out

        return await gateway.run("generate_signal", args, call)

    @mcp.tool(
        title="Portfolio summary",
        description="Paper account equity, whether the market is open, and the verified paper mode.",
        annotations=READ_ONLY,
    )
    async def get_portfolio_summary() -> dict[str, Any]:
        async def call() -> dict[str, Any]:
            body = await up.request("engine", "GET", "/api/v1/portfolio/equity")
            return {
                "equity": body.get("equity"),
                "market_open": body.get("market_open"),
                "paper_mode": body.get("paper_mode"),
            }

        return await gateway.run("get_portfolio_summary", {}, call)

    @mcp.tool(
        title="Open positions",
        description="Open positions in the paper account (symbol, quantity, prices, unrealized P&L).",
        annotations=READ_ONLY,
    )
    async def get_positions() -> dict[str, Any]:
        async def call() -> dict[str, Any]:
            body = await up.request("engine", "GET", "/api/v1/portfolio/positions")
            positions = body if isinstance(body, list) else []
            keys = ("symbol", "quantity", "average_entry_price", "current_price", "unrealized_pnl")
            items = [{k: p.get(k) for k in keys} for p in positions if isinstance(p, dict)]
            return {"positions": items, "count": len(items)}

        return await gateway.run("get_positions", {}, call)

    @mcp.tool(
        title="Trading mode",
        description=(
            "The trading mode verified by bot-engine at startup (paper only) and whether "
            "order execution is enabled."
        ),
        annotations=READ_ONLY,
    )
    async def get_trading_mode() -> dict[str, Any]:
        async def call() -> dict[str, Any]:
            body = await up.request("engine", "GET", "/api/v1/trading-mode")
            keys = ("mode", "verified", "broker_host", "order_execution_enabled")
            return {k: body.get(k) for k in keys}

        return await gateway.run("get_trading_mode", {}, call)

    @mcp.tool(
        title="Propose order intent",
        description=(
            "PROPOSE a paper-trading order for a US stock based on a strategy signal. This tool "
            "only creates a proposal (an order intent): it executes NOTHING and places no order. "
            "bot-engine fetches the signal itself, sizes the order and runs risk checks; a HUMAN "
            "must then approve the intent before any order is submitted, and unapproved intents "
            "expire. Paper trading only. You cannot choose side or quantity. Pass the returned "
            "idempotency_key again to retry safely after a timeout (the same key and arguments "
            "return the original intent instead of creating a second one). " + DISCLAIMER
        ),
        annotations=PROPOSE,
    )
    async def create_order_intent(
        symbol: Annotated[str, Field(description="Ticker, 1-10 letters, e.g. AAPL")],
        strategy: Annotated[str, Field(description="Strategy name, e.g. rsi or ma_cross")],
        idempotency_key: Annotated[
            str | None,
            Field(
                max_length=128,
                description=(
                    "Optional retry key, 8-128 chars of A-Z a-z 0-9 . _ : -. Omit it on the first "
                    "call: the gateway generates one and returns it."
                ),
            ),
        ] = None,
    ) -> dict[str, Any]:
        generated = idempotency_key is None
        key = str(uuid.uuid4()) if idempotency_key is None else idempotency_key
        args = {
            "symbol": symbol,
            "strategy": strategy,
            "idempotency_key": key,
            "idempotency_key_generated": generated,
        }

        async def call() -> dict[str, Any]:
            payload = {"symbol": _symbol(symbol), "strategy": _strategy(strategy)}
            headers = {"Idempotency-Key": _idempotency_key(key)}
            try:
                resp = await up.send(
                    "engine", "POST", ORDER_INTENTS_PATH, json=payload, headers=headers
                )
            except UpstreamError as exc:
                if exc.status is None or exc.status >= 500:
                    # The outcome may be unknown: retrying with the same key cannot duplicate it.
                    exc.message += f"; retry with idempotency_key={key}"
                raise
            replayed = resp.headers.get("idempotent-replayed", "").strip().lower() == "true"
            return {
                "intent": _shape_intent(resp.body),
                "idempotency_key": key,
                "idempotent_replayed": replayed,
                "requires_human_approval": True,
                "executed": False,
                "note": INTENT_NOTE,
            }

        return await gateway.run("create_order_intent", args, call)

    @mcp.tool(
        title="Get order intent",
        description=(
            "Read one order intent (a proposed paper order) by id: its status, sizing, risk "
            "checks and, once a human approved it, the paper order. Read-only."
        ),
        annotations=READ_ONLY,
    )
    async def get_order_intent(
        intent_id: Annotated[str, Field(max_length=64, description="Intent id (UUID)")],
    ) -> dict[str, Any]:
        async def call() -> dict[str, Any]:
            iid = _intent_id(intent_id)
            try:
                body = await up.request(
                    "engine", "GET", ORDER_INTENT_PATH, path_params={"id": iid}
                )
            except UpstreamError as exc:
                if exc.code == "not_found":
                    exc.message = "no order intent with this id"
                raise
            return _shape_intent(body)

        return await gateway.run("get_order_intent", {"intent_id": intent_id}, call)

    @mcp.tool(
        title="List order intents",
        description=(
            "List order intents (proposed paper orders), newest first, optionally filtered by "
            "status, e.g. pending_approval for those waiting for a human. Read-only."
        ),
        annotations=READ_ONLY,
    )
    async def list_order_intents(
        status: Annotated[IntentStatus | None, Field(description="Only intents in this status")] = None,
        limit: Annotated[int, Field(ge=1, le=200, description="Max intents (1-200)")] = 50,
    ) -> dict[str, Any]:
        args = {"status": status, "limit": limit}

        async def call() -> dict[str, Any]:
            if not 1 <= limit <= 200:
                raise InputError("limit must be between 1 and 200")
            params: dict[str, str | int] = {"limit": limit}
            st = _intent_status(status)
            if st is not None:
                params["status"] = st
            body = await up.request("engine", "GET", ORDER_INTENTS_PATH, params=params)
            raw = body.get("intents") if isinstance(body, dict) else None
            if not isinstance(raw, list):
                raise UpstreamError("upstream_bad_response", "bot-engine returned an unexpected list body")
            intents = [_shape_intent(i) for i in raw]
            return {"intents": intents, "count": len(intents)}

        return await gateway.run("list_order_intents", args, call)

    return mcp
