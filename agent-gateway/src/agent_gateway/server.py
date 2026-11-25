"""MCP server exposing read-only market-analysis tools (ADR-002, M1).

Tools map 1:1 to read endpoints of signal-service and bot-engine. There is no order,
cancel or modify capability, and the gateway's service token only holds read scopes,
so bot-engine rejects it on every write route even if this code were changed.
"""

from __future__ import annotations

import re
import time
import uuid
from collections.abc import AsyncIterator, Awaitable, Callable
from contextlib import asynccontextmanager
from typing import Annotated, Any, Literal

from mcp.server.mcpserver import MCPServer
from mcp.server.mcpserver.exceptions import ToolError
from mcp.types import ToolAnnotations
from pydantic import Field

from .audit import AuditLog
from .config import GatewayConfig
from .ratelimit import RateLimiter
from .upstream import Upstream, UpstreamError, current_request_id

INSTRUCTIONS = (
    "Read-only market analysis for a PAPER-TRADING account. These tools can list "
    "strategies, compute RSI / moving-average-cross signals on recent bars, and read the "
    "paper portfolio and the verified trading mode. They cannot place, modify or cancel "
    "orders. Signals are algorithmic indicators, not financial advice: always present them "
    "with that caveat."
)
DISCLAIMER = "Algorithmic indicator on a paper-trading account; not financial advice."

Timeframe = Literal["1Min", "5Min", "15Min", "30Min", "1Hour", "4Hour", "1Day"]
_SYMBOL_RE = re.compile(r"^[A-Z]{1,10}$")
_STRATEGY_RE = re.compile(r"^[a-z][a-z0-9_]{0,31}$")

READ_ONLY = ToolAnnotations(
    read_only_hint=True, destructive_hint=False, idempotent_hint=True, open_world_hint=True
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
        title="Signal Trading (read-only, paper)",
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

    return mcp
