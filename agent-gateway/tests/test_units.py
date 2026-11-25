"""Config, endpoint allowlist, rate limiter, audit file."""

from __future__ import annotations

import json
from pathlib import Path

import httpx
import pytest

from agent_gateway.audit import AuditLog
from agent_gateway.config import ConfigError, GatewayConfig
from agent_gateway.ratelimit import RateLimiter
from agent_gateway.upstream import ALLOWED_ENDPOINTS, Upstream, UpstreamError

pytestmark = pytest.mark.anyio

def test_config_requires_token() -> None:
    with pytest.raises(ConfigError, match="GATEWAY_TOKEN"):
        GatewayConfig.from_env({})


def test_config_defaults_and_repr_hides_token() -> None:
    c = GatewayConfig.from_env({"GATEWAY_TOKEN": "s3cret"})
    assert c.signal_service_url == "http://127.0.0.1:8000"
    assert c.bot_engine_url == "http://127.0.0.1:8080"
    assert c.agent_id == "mcp-agent"
    assert "s3cret" not in repr(c)


@pytest.mark.parametrize(
    "env,match",
    [
        ({"BOT_ENGINE_URL": "http://10.0.0.5:8080"}, "https unless it points to localhost"),
        ({"SIGNAL_SERVICE_URL": "ftp://localhost"}, "http\\(s\\) URL"),
        ({"BOT_ENGINE_URL": "https://u:p@engine.example.com"}, "credentials"),
        ({"AGENT_ID": "bad id!"}, "AGENT_ID"),
        ({"RATE_LIMIT_PER_MINUTE": "0"}, "> 0"),
        ({"REQUEST_TIMEOUT_SECONDS": "abc"}, "number"),
    ],
)
def test_config_validation(env: dict[str, str], match: str) -> None:
    with pytest.raises(ConfigError, match=match):
        GatewayConfig.from_env({"GATEWAY_TOKEN": "t", **env})


def test_config_accepts_https_remote() -> None:
    c = GatewayConfig.from_env({"GATEWAY_TOKEN": "t", "BOT_ENGINE_URL": "https://engine.example.com/"})
    assert c.bot_engine_url == "https://engine.example.com"


def test_allowlist_contains_only_reads_and_intent_proposal() -> None:
    writes = {(s, m, p) for s, m, p in ALLOWED_ENDPOINTS if m != "GET"}
    # Computing a signal and proposing an intent (intent:create) are the only non-GET routes.
    assert writes == {("signal", "POST", "/api/v1/signals/generate"), ("engine", "POST", "/api/v1/order-intents")}
    for _, _, path in ALLOWED_ENDPOINTS:
        for word in ("approve", "reject", "cancel", "kill", "orders", "admin"):
            assert word not in path


@pytest.mark.parametrize(
    "service,method,path",
    [
        ("engine", "POST", "/api/v1/orders/place"),
        ("engine", "GET", "/ws"),
        ("signal", "GET", "/api/v1/portfolio/equity"),
        ("engine", "DELETE", "/api/v1/portfolio/positions"),
        ("engine", "POST", "/api/v1/order-intents/{id}/approve"),
        ("engine", "POST", "/api/v1/order-intents/{id}/reject"),
        ("engine", "POST", "/api/v1/order-intents/{id}/cancel"),
        ("engine", "POST", "/api/v1/kill-switch"),
        ("engine", "POST", "/api/v1/order-intents/{id}"),
        ("engine", "DELETE", "/api/v1/order-intents/{id}"),
        ("signal", "POST", "/api/v1/order-intents"),
    ],
)
async def test_non_allowlisted_endpoints_are_refused_without_a_request(service: str, method: str, path: str) -> None:
    calls: list[httpx.Request] = []
    up = Upstream(GatewayConfig(gateway_token="t"), transport=httpx.MockTransport(lambda r: calls.append(r) or httpx.Response(200, json={})))
    with pytest.raises(UpstreamError) as exc_info:
        await up.request(service, method, path)  # type: ignore[arg-type]
    assert exc_info.value.code == "endpoint_not_allowed"
    assert calls == []
    await up.aclose()


@pytest.mark.parametrize(
    "path_params",
    [None, {}, {"id": "../approve"}, {"id": "a/b"}, {"id": "x?y=1"}, {"id": "abc", "extra": "1"}],
)
async def test_path_parameters_cannot_reach_other_routes(path_params: dict[str, str] | None) -> None:
    calls: list[httpx.Request] = []
    up = Upstream(GatewayConfig(gateway_token="t"), transport=httpx.MockTransport(lambda r: calls.append(r) or httpx.Response(200, json={})))
    with pytest.raises(UpstreamError) as exc_info:
        await up.request("engine", "GET", "/api/v1/order-intents/{id}", path_params=path_params)
    assert exc_info.value.code == "endpoint_not_allowed"
    assert calls == []
    await up.aclose()


async def test_redirects_are_not_followed() -> None:
    def handler(r: httpx.Request) -> httpx.Response:
        return httpx.Response(302, headers={"location": "https://evil.example.com/steal"})

    up = Upstream(GatewayConfig(gateway_token="t"), transport=httpx.MockTransport(handler))
    with pytest.raises(UpstreamError, match="HTTP 302"):
        await up.request("engine", "GET", "/api/v1/trading-mode")
    await up.aclose()


async def test_invalid_json_response() -> None:
    up = Upstream(GatewayConfig(gateway_token="t"), transport=httpx.MockTransport(lambda r: httpx.Response(200, text="<html>")))
    with pytest.raises(UpstreamError, match="invalid JSON"):
        await up.request("engine", "GET", "/api/v1/trading-mode")
    await up.aclose()


def test_rate_limiter_is_per_agent() -> None:
    now = [0.0]
    rl = RateLimiter(1, clock=lambda: now[0])
    assert rl.allow("a") and not rl.allow("a")
    assert rl.allow("b")
    assert rl.retry_after("a") == pytest.approx(60.0)
    now[0] = 60.0
    assert rl.allow("a")


def test_audit_file_append(tmp_path: Path) -> None:
    f = tmp_path / "audit.jsonl"
    import io

    log = AuditLog("agent-x", file_path=str(f), stream=io.StringIO())
    log.record("list_strategies", {}, "ok", 1.234)
    log.record("get_positions", {}, "error", 2.0, "upstream_timeout")
    lines = [json.loads(x) for x in f.read_text(encoding="utf-8").splitlines()]
    assert [x["tool"] for x in lines] == ["list_strategies", "get_positions"]
    assert lines[1]["error_code"] == "upstream_timeout" and lines[0]["duration_ms"] == 1.2



def test_request_id_fits_service_limit_with_longest_agent_id() -> None:
    import re

    from agent_gateway.server import new_request_id

    rid = new_request_id("a" * 64)  # longest AGENT_ID the config accepts
    assert re.fullmatch(r"[A-Za-z0-9._-]{1,64}", rid)
    assert new_request_id("x") != new_request_id("x")
