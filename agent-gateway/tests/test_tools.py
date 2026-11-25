"""MCP tool behaviour through a real MCP client session (in-process transport)."""

from __future__ import annotations

import io
from collections.abc import Callable
from typing import Any

import httpx
import pytest
from mcp import Client
from tests.conftest import TOKEN, FakeServices, audit_lines

pytestmark = pytest.mark.anyio

EXPECTED_TOOLS = {"list_strategies", "generate_signal", "get_portfolio_summary", "get_positions", "get_trading_mode"}


def text(result: Any) -> str:
    return " ".join(getattr(c, "text", "") for c in result.content)


async def test_only_read_only_tools_are_exposed(client: Client) -> None:
    tools = (await client.list_tools()).tools
    assert {t.name for t in tools} == EXPECTED_TOOLS
    for t in tools:
        assert t.annotations is not None and t.annotations.read_only_hint is True
        assert t.annotations.destructive_hint is False
        for word in ("order", "cancel", "submit", "buy", "sell", "trade"):
            assert word not in t.name


async def test_list_strategies(client: Client, services: FakeServices) -> None:
    r = await client.call_tool("list_strategies", {})
    assert not r.is_error
    assert r.structured_content == {"strategies": ["ma_cross", "rsi"]}
    assert services.requests[0].headers["authorization"] == f"Bearer {TOKEN}"


async def test_generate_signal_normalizes_and_shapes(client: Client, services: FakeServices, audit_stream: io.StringIO) -> None:
    r = await client.call_tool(
        "generate_signal", {"symbol": " aapl ", "strategy": "RSI", "timeframe": "1Day", "bars": 60}
    )
    assert not r.is_error, text(r)
    req = services.requests[0]
    assert req.method == "POST" and req.url.path == "/api/v1/signals/generate"
    assert req.read() == b'{"symbol":"AAPL","strategy":"rsi","timeframe":"1Day","bars_limit":60}'
    out = r.structured_content
    assert out is not None
    assert out["direction"] == "BUY" and out["symbol"] == "AAPL" and out["timeframe"] == "1Day"
    assert "not financial advice" in out["disclaimer"]
    assert "id" not in out
    [line] = audit_lines(audit_stream)
    assert line["tool"] == "generate_signal" and line["outcome"] == "ok"
    assert line["actor_id"] == "test-agent" and line["actor_type"] == "agent"
    assert line["args"]["symbol"] == " aapl "


async def test_generate_signal_defaults(client: Client, services: FakeServices) -> None:
    r = await client.call_tool("generate_signal", {"symbol": "MSFT"})
    assert not r.is_error, text(r)
    assert services.requests[0].read() == b'{"symbol":"MSFT","strategy":"rsi","timeframe":"1Hour","bars_limit":100}'


@pytest.mark.parametrize(
    "args",
    [
        {"symbol": "AAPL1"},
        {"symbol": ""},
        {"symbol": "AAPL", "strategy": "rsi; DROP"},
        {"symbol": "AAPL", "strategy": "../../orders"},
    ],
)
async def test_invalid_input_never_reaches_upstream(
    client: Client, services: FakeServices, audit_stream: io.StringIO, args: dict[str, Any]
) -> None:
    r = await client.call_tool("generate_signal", args)
    assert r.is_error
    assert "invalid_input" in text(r)
    assert services.requests == []
    assert audit_lines(audit_stream)[-1]["error_code"] == "invalid_input"


@pytest.mark.parametrize(
    "args",
    [{"symbol": "AAPL", "bars": 5}, {"symbol": "AAPL", "bars": 5000}, {"symbol": "AAPL", "timeframe": "2Hour"}, {}],
)
async def test_schema_validation(client: Client, services: FakeServices, args: dict[str, Any]) -> None:
    r = await client.call_tool("generate_signal", args)
    assert r.is_error
    assert services.requests == []


async def test_portfolio_summary_drops_unexpected_fields(client: Client) -> None:
    r = await client.call_tool("get_portfolio_summary", {})
    assert r.structured_content == {"equity": 100000.0, "market_open": False, "paper_mode": True}
    assert "SHOULD-NOT-LEAK" not in text(r)


async def test_positions(client: Client) -> None:
    r = await client.call_tool("get_positions", {})
    out = r.structured_content
    assert out is not None and out["count"] == 1
    assert out["positions"][0] == {
        "symbol": "AAPL", "quantity": 3, "average_entry_price": 180.0, "current_price": 187.5, "unrealized_pnl": 22.5,
    }
    assert "SHOULD-NOT-LEAK" not in text(r)


async def test_trading_mode(client: Client) -> None:
    r = await client.call_tool("get_trading_mode", {})
    assert r.structured_content == {
        "mode": "paper", "verified": True, "broker_host": "paper-api.alpaca.markets", "order_execution_enabled": False,
    }


@pytest.mark.parametrize(
    "status,body,code",
    [
        (401, {"error": "unauthorized"}, "gateway_unauthorized"),
        (403, {"error": "insufficient_scope", "required_scope": "portfolio:read"}, "requires portfolio:read"),
        (500, {"error": "alpaca: 403 forbidden key=PKSECRETLEAK account=12345"}, "bot-engine returned HTTP 500"),
        (503, {"detail": "Market data unavailable for AAPL: raw alpaca text PKSECRETLEAK"}, "market_data_unavailable"),
        (429, {}, "upstream_rate_limited"),
        (502, {"error": "broker_unavailable", "detail": "the broker did not return the account equity",
               "request_id": "x"}, "broker_unavailable"),
    ],
)
async def test_upstream_errors_are_sanitized(
    client: Client, services: FakeServices, audit_stream: io.StringIO, status: int, body: Any, code: str
) -> None:
    services.set("127.0.0.1:8080", "GET", "/api/v1/portfolio/equity", status, body)
    r = await client.call_tool("get_portfolio_summary", {})
    assert r.is_error
    msg = text(r)
    assert code in msg
    assert "PKSECRETLEAK" not in msg and "12345" not in msg
    assert "PKSECRETLEAK" not in audit_stream.getvalue()


async def test_422_detail_is_passed_through(client: Client, services: FakeServices) -> None:
    services.set("127.0.0.1:8000", "POST", "/api/v1/signals/generate", 422,
                 {"detail": "Strategy 'foo' is not registered. Available: ma_cross, rsi."})
    r = await client.call_tool("generate_signal", {"symbol": "AAPL", "strategy": "foo"})
    assert r.is_error and "Available: ma_cross, rsi" in text(r)


@pytest.mark.parametrize(
    "exc,code",
    [(httpx.ConnectError("refused"), "upstream_unreachable"), (httpx.ReadTimeout("slow"), "upstream_timeout")],
)
async def test_transport_failures(client: Client, services: FakeServices, exc: Exception, code: str) -> None:
    services.raise_exc = exc
    r = await client.call_tool("get_trading_mode", {})
    assert r.is_error and code in text(r)


async def test_rate_limit(make_client: Callable[..., Client], audit_stream: io.StringIO) -> None:
    now = [0.0]
    async with make_client(per_minute=2, clock=lambda: now[0]) as c:
        assert not (await c.call_tool("list_strategies", {})).is_error
        assert not (await c.call_tool("list_strategies", {})).is_error
        r = await c.call_tool("list_strategies", {})
        assert r.is_error and "rate_limited" in text(r)
        now[0] = 30.0  # 2/min refills one token every 30s
        assert not (await c.call_tool("list_strategies", {})).is_error
    outcomes = [line["outcome"] for line in audit_lines(audit_stream)]
    assert outcomes == ["ok", "ok", "rate_limited", "ok"]


async def test_audit_never_contains_the_token(client: Client, audit_stream: io.StringIO) -> None:
    for tool in EXPECTED_TOOLS - {"generate_signal"}:
        await client.call_tool(tool, {})
    await client.call_tool("generate_signal", {"symbol": "AAPL"})
    assert TOKEN not in audit_stream.getvalue()
    assert len(audit_lines(audit_stream)) == 5


async def test_each_call_sends_request_id_and_audits_it(
    client: Client, services: FakeServices, audit_stream: io.StringIO
) -> None:
    await client.call_tool("get_trading_mode", {})
    await client.call_tool("list_strategies", {})
    sent = [r.headers.get("x-request-id") for r in services.requests]
    audited = [line["request_id"] for line in audit_lines(audit_stream)]
    assert len(set(sent)) == 2 and sent == audited
    for rid in sent:
        assert rid.startswith("test-agent-") and len(rid) <= 64
