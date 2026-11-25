"""End-to-end: the real `python -m agent_gateway` process over STDIO, real HTTP to a local fake."""

from __future__ import annotations

import json
import os
import subprocess
import sys
import threading
from collections.abc import Iterator
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any

import pytest
from mcp import Client, StdioServerParameters

pytestmark = pytest.mark.anyio

TOKEN = "e2e-gateway-token"


class _Handler(BaseHTTPRequestHandler):
    seen: list[tuple[str, str, str]] = []

    def _reply(self) -> None:
        _Handler.seen.append((self.command, self.path, self.headers.get("Authorization", "")))
        if self.headers.get("Authorization") != f"Bearer {TOKEN}":
            body: Any = {"error": "unauthorized"}
            status = 401
        elif (self.command, self.path) == ("GET", "/api/v1/trading-mode"):
            status, body = 200, {"mode": "paper", "verified": True, "broker_host": "paper-api.alpaca.markets",
                                 "order_execution_enabled": False}
        elif (self.command, self.path) == ("GET", "/api/v1/signals/strategies"):
            status, body = 200, {"strategies": ["ma_cross", "rsi"]}
        else:
            status, body = 404, {"detail": "Not Found"}
        data = json.dumps(body).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    do_GET = _reply
    do_POST = _reply

    def log_message(self, *args: Any) -> None:
        pass


@pytest.fixture
def anyio_backend() -> str:
    return "asyncio"


@pytest.fixture
def fake_backend() -> Iterator[str]:
    srv = ThreadingHTTPServer(("127.0.0.1", 0), _Handler)
    t = threading.Thread(target=srv.serve_forever, daemon=True)
    t.start()
    yield f"http://127.0.0.1:{srv.server_address[1]}"
    srv.shutdown()


def _env(**extra: str) -> dict[str, str]:
    keep = {k: v for k, v in os.environ.items() if k.upper() in {"PATH", "SYSTEMROOT", "TEMP", "TMP", "PYTHONPATH"}}
    return {**keep, **extra}


async def test_stdio_session_end_to_end(fake_backend: str, tmp_path: Any) -> None:
    audit_file = tmp_path / "audit.jsonl"
    params = StdioServerParameters(
        command=sys.executable,
        args=["-m", "agent_gateway"],
        env=_env(GATEWAY_TOKEN=TOKEN, AGENT_ID="e2e", SIGNAL_SERVICE_URL=fake_backend,
                 BOT_ENGINE_URL=fake_backend, AUDIT_LOG_FILE=str(audit_file)),
    )
    async with Client(params) as client:
        tools = {t.name for t in (await client.list_tools()).tools}
        assert tools == {"list_strategies", "generate_signal", "get_portfolio_summary", "get_positions", "get_trading_mode",
                         "create_order_intent", "get_order_intent", "list_order_intents"}
        mode = await client.call_tool("get_trading_mode", {})
        assert not mode.is_error
        assert mode.structured_content is not None and mode.structured_content["mode"] == "paper"
        strategies = await client.call_tool("list_strategies", {})
        assert strategies.structured_content == {"strategies": ["ma_cross", "rsi"]}

    assert all(auth == f"Bearer {TOKEN}" for _, _, auth in _Handler.seen)
    lines = [json.loads(x) for x in audit_file.read_text(encoding="utf-8").splitlines()]
    assert [x["tool"] for x in lines] == ["get_trading_mode", "list_strategies"]
    assert TOKEN not in audit_file.read_text(encoding="utf-8")


def test_missing_token_exits_cleanly_without_stdout() -> None:
    proc = subprocess.run(
        [sys.executable, "-m", "agent_gateway"], env=_env(), capture_output=True, text=True, timeout=60,
        stdin=subprocess.DEVNULL,
    )
    assert proc.returncode == 2
    assert proc.stdout == ""
    assert "GATEWAY_TOKEN is required" in proc.stderr
