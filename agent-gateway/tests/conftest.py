"""Fake signal-service / bot-engine over httpx.MockTransport, plus an in-process MCP client."""

from __future__ import annotations

import io
import json
from collections.abc import AsyncIterator, Callable
from dataclasses import dataclass, field
from typing import Any

import httpx
import pytest
from mcp import Client

from agent_gateway.audit import AuditLog
from agent_gateway.config import GatewayConfig
from agent_gateway.ratelimit import RateLimiter
from agent_gateway.server import Gateway, build_server
from agent_gateway.upstream import Upstream

TOKEN = "gw-secret-token-for-tests"

SIGNAL_BODY = {
    "id": "6f1c0b8e-0000-4000-8000-000000000001",
    "symbol": "AAPL",
    "direction": "BUY",
    "strength": "STRONG",
    "price": 187.5,
    "confidence": 0.91,
    "reason": "RSI=21.3 - strongly oversold (threshold=25.0)",
    "strategy_name": "RSIStrategy(period=14)",
    "is_actionable": True,
    "generated_at": "2026-11-24T15:00:00Z",
}


@dataclass
class FakeServices:
    """Routes keyed by (host, method, path) → (status, json body)."""

    routes: dict[tuple[str, str, str], tuple[int, Any]] = field(default_factory=dict)
    requests: list[httpx.Request] = field(default_factory=list)
    raise_exc: Exception | None = None

    def __post_init__(self) -> None:
        self.routes.update(
            {
                ("127.0.0.1:8000", "GET", "/api/v1/signals/strategies"): (200, {"strategies": ["ma_cross", "rsi"]}),
                ("127.0.0.1:8000", "POST", "/api/v1/signals/generate"): (200, SIGNAL_BODY),
                ("127.0.0.1:8080", "GET", "/api/v1/portfolio/equity"): (
                    200, {"equity": 100000.0, "paper_mode": True, "market_open": False, "account_id": "SHOULD-NOT-LEAK"},
                ),
                ("127.0.0.1:8080", "GET", "/api/v1/portfolio/positions"): (
                    200,
                    [{"symbol": "AAPL", "quantity": 3, "average_entry_price": 180.0, "current_price": 187.5,
                      "unrealized_pnl": 22.5, "asset_id": "SHOULD-NOT-LEAK"}],
                ),
                ("127.0.0.1:8080", "GET", "/api/v1/trading-mode"): (
                    200, {"mode": "paper", "verified": True, "broker_host": "paper-api.alpaca.markets",
                          "order_execution_enabled": False},
                ),
            }
        )

    def handler(self, request: httpx.Request) -> httpx.Response:
        self.requests.append(request)
        if self.raise_exc is not None:
            raise self.raise_exc
        key = (request.url.netloc.decode(), request.method, request.url.path)
        status, body = self.routes.get(key, (404, {"detail": "Not Found"}))
        return httpx.Response(status, json=body)

    def set(self, host: str, method: str, path: str, status: int, body: Any) -> None:
        self.routes[(host, method, path)] = (status, body)


@pytest.fixture
def anyio_backend() -> str:
    return "asyncio"


@pytest.fixture
def services() -> FakeServices:
    return FakeServices()


@pytest.fixture
def audit_stream() -> io.StringIO:
    return io.StringIO()


def audit_lines(stream: io.StringIO) -> list[dict[str, Any]]:
    return [json.loads(line) for line in stream.getvalue().splitlines() if line.strip()]


@pytest.fixture
def make_client(services: FakeServices, audit_stream: io.StringIO) -> Callable[..., Client]:
    def factory(per_minute: float = 600, clock: Callable[[], float] | None = None) -> Client:
        config = GatewayConfig(gateway_token=TOKEN, agent_id="test-agent", rate_limit_per_minute=per_minute)
        gateway = Gateway(
            config,
            upstream=Upstream(config, transport=httpx.MockTransport(services.handler)),
            audit=AuditLog("test-agent", stream=audit_stream),
            limiter=RateLimiter(per_minute, clock=clock) if clock else RateLimiter(per_minute),
        )
        return Client(build_server(gateway), raise_exceptions=False)

    return factory


@pytest.fixture
async def client(make_client: Callable[..., Client]) -> AsyncIterator[Client]:
    async with make_client() as c:
        yield c
