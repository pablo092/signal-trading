"""HTTP route tests with a fake IMarketDataRepository (BL-04) and auth (BL-06)."""

from __future__ import annotations

import json
from collections.abc import Iterator

import pandas as pd
import pytest
from fastapi.routing import APIRoute
from fastapi.testclient import TestClient
from src.api.routes import signals as signals_router
from src.domain.entities.exceptions import MarketDataUnavailableError
from src.domain.ports import IMarketDataRepository
from src.domain.value_objects import Symbol, Timeframe
from src.infrastructure.auth import TokenTableError, hash_token
from src.infrastructure.config import Settings
from src.main import create_app

from tests.conftest import make_bars, make_oversold_bars

AGENT = "agent-token-for-tests"
OTHER = "portfolio-only-token"

PUBLIC_PATHS = {"/health/live", "/health/ready", "/docs", "/docs/oauth2-redirect", "/redoc", "/openapi.json"}


class FakeMarketData(IMarketDataRepository):
    def __init__(self, bars: pd.DataFrame | None = None, error: Exception | None = None) -> None:
        self.bars = bars if bars is not None else make_oversold_bars(n=120)
        self.error = error
        self.calls: list[tuple[str, str, int]] = []

    async def get_bars(self, symbol: Symbol, timeframe: Timeframe, limit: int = 100) -> pd.DataFrame:
        self.calls.append((symbol.ticker, timeframe.value, limit))
        if self.error:
            raise self.error
        return self.bars.copy()


def make_settings(tokens: list[dict[str, object]] | None = None) -> Settings:
    table = tokens if tokens is not None else [
        {"id": "mcp-gateway", "type": "agent", "sha256": hash_token(AGENT), "scopes": ["market:read", "portfolio:read"]},
        {"id": "viewer", "type": "human", "sha256": hash_token(OTHER), "scopes": ["portfolio:read"]},
    ]
    return Settings(
        _env_file=None,  # type: ignore[call-arg]
        ALPACA_API_KEY="fake",
        ALPACA_SECRET_KEY="fake",
        AUTH_TOKENS=json.dumps(table),
    )


@pytest.fixture
def fake() -> FakeMarketData:
    return FakeMarketData()


@pytest.fixture
def client(fake: FakeMarketData) -> Iterator[TestClient]:
    app = create_app(make_settings())
    app.dependency_overrides[signals_router.get_market_data] = lambda: fake
    with TestClient(app) as c:
        yield c


def auth(token: str = AGENT) -> dict[str, str]:
    return {"Authorization": f"Bearer {token}"}


GEN = "/api/v1/signals/generate"


def test_health_is_public(client: TestClient) -> None:
    for path in ("/health/live", "/health/ready"):
        r = client.get(path)
        assert r.status_code == 200
        assert r.json()["paper_trading"] is True


def test_route_scope_table_every_route_is_protected(client: TestClient) -> None:
    """Deny by default: every API route outside the explicit public set requires a scope."""
    app = client.app
    for route in app.routes:  # type: ignore[attr-defined]
        if not isinstance(route, APIRoute) or route.path in PUBLIC_PATHS:
            continue
        scopes = [getattr(d.call, "required_scope", None) for d in route.dependant.dependencies]
        assert any(scopes), f"{route.methods} {route.path} declares no scope"


@pytest.mark.parametrize("method,path", [("POST", GEN), ("GET", "/api/v1/signals/strategies")])
@pytest.mark.parametrize("headers", [{}, {"Authorization": "Bearer wrong"}, {"Authorization": "Basic abc"}])
def test_protected_routes_require_token(client: TestClient, fake: FakeMarketData, method: str, path: str, headers: dict[str, str]) -> None:
    r = client.request(method, path, headers=headers, json={"symbol": "AAPL", "strategy": "rsi"})
    assert r.status_code == 401
    assert fake.calls == []


def test_scope_enforced(client: TestClient, fake: FakeMarketData) -> None:
    r = client.post(GEN, headers=auth(OTHER), json={"symbol": "AAPL", "strategy": "rsi"})
    assert r.status_code == 403
    assert r.json()["detail"]["error"] == "insufficient_scope"
    assert fake.calls == []


def test_list_strategies(client: TestClient) -> None:
    r = client.get("/api/v1/signals/strategies", headers=auth())
    assert r.status_code == 200
    assert r.json()["strategies"] == ["ma_cross", "rsi"]


def test_generate_rsi_buy(client: TestClient, fake: FakeMarketData) -> None:
    r = client.post(GEN, headers=auth(), json={"symbol": "AAPL", "strategy": "RSI", "timeframe": "1Day", "bars_limit": 50})
    assert r.status_code == 200, r.text
    body = r.json()
    assert body["symbol"] == "AAPL"
    assert body["direction"] == "BUY"
    assert body["is_actionable"] is True
    assert "RSIStrategy" in body["strategy_name"]
    assert 0 <= body["confidence"] <= 1
    assert fake.calls == [("AAPL", "1Day", 50)]


def test_generate_ma_cross(client: TestClient) -> None:
    r = client.post(GEN, headers=auth(), json={"symbol": "MSFT", "strategy": "ma_cross"})
    assert r.status_code == 200, r.text
    assert r.json()["direction"] in ("BUY", "SELL", "HOLD")
    assert r.json()["strategy_name"].startswith("MACrossStrategy(EMA")


def test_unknown_strategy_is_422_and_lists_available(client: TestClient) -> None:
    r = client.post(GEN, headers=auth(), json={"symbol": "AAPL", "strategy": "nope"})
    assert r.status_code == 422
    assert r.json()["detail"] == "Strategy 'nope' is not registered. Available: ma_cross, rsi."


def test_insufficient_data_is_422(fake: FakeMarketData, client: TestClient) -> None:
    fake.bars = make_bars(n=5)
    r = client.post(GEN, headers=auth(), json={"symbol": "AAPL", "strategy": "rsi"})
    assert r.status_code == 422
    assert "at least" in r.json()["detail"]


def test_market_data_unavailable_is_503(fake: FakeMarketData, client: TestClient) -> None:
    fake.error = MarketDataUnavailableError(symbol="AAPL", reason="timeout")
    r = client.post(GEN, headers=auth(), json={"symbol": "AAPL", "strategy": "rsi"})
    assert r.status_code == 503


@pytest.mark.parametrize(
    "body",
    [
        {"symbol": "aapl", "strategy": "rsi"},
        {"symbol": "AAPL1", "strategy": "rsi"},
        {"symbol": "AAPL", "strategy": "rsi", "bars_limit": 5},
        {"symbol": "AAPL", "strategy": "rsi", "bars_limit": 5000},
        {"symbol": "AAPL", "strategy": "rsi", "timeframe": "2Hour"},
        {"strategy": "rsi"},
    ],
)
def test_request_validation(client: TestClient, fake: FakeMarketData, body: dict[str, object]) -> None:
    r = client.post(GEN, headers=auth(), json=body)
    assert r.status_code == 422
    assert fake.calls == []


def test_no_tokens_configured_denies_everything(fake: FakeMarketData) -> None:
    app = create_app(make_settings(tokens=[]))
    app.dependency_overrides[signals_router.get_market_data] = lambda: fake
    with TestClient(app) as c:
        assert c.get("/api/v1/signals/strategies", headers=auth()).status_code == 401
        assert c.get("/health/live").status_code == 200


def test_invalid_token_table_fails_startup() -> None:
    with pytest.raises(TokenTableError):
        create_app(make_settings(tokens=[{"id": "gw", "type": "agent", "sha256": hash_token("t"), "scopes": ["intent:approve"]}]))


def test_metrics_endpoint(client: TestClient) -> None:
    r = client.get("/metrics/")
    assert r.status_code == 200
    assert "http_requests_total" in r.text or "python_info" in r.text
