"""Error envelope and request IDs (BL-20)."""

from __future__ import annotations

from collections.abc import Iterator

import pytest
from fastapi.testclient import TestClient
from src.api.routes import signals as signals_router
from src.domain.entities.exceptions import MarketDataUnavailableError
from src.main import create_app

from tests.test_api import GEN, OTHER, FakeMarketData, auth, make_settings


@pytest.fixture
def fake() -> FakeMarketData:
    return FakeMarketData()


@pytest.fixture
def client(fake: FakeMarketData) -> Iterator[TestClient]:
    app = create_app(make_settings())
    app.dependency_overrides[signals_router.get_market_data] = lambda: fake
    # The 500 handler must answer, not re-raise into the test.
    with TestClient(app, raise_server_exceptions=False) as c:
        yield c


def assert_envelope(r, status: int, code: str) -> dict[str, object]:  # type: ignore[no-untyped-def]
    assert r.status_code == status, r.text
    body = r.json()
    assert set(body) >= {"error", "detail", "request_id"}, body
    assert body["error"] == code
    assert isinstance(body["detail"], str) and body["detail"]
    assert body["request_id"] == r.headers["x-request-id"]
    return body


@pytest.mark.parametrize(
    "kwargs,status,code",
    [
        ({"headers": {}}, 401, "unauthorized"),
        ({"headers": auth(OTHER)}, 403, "insufficient_scope"),
        ({"headers": auth(), "json": {"symbol": "AAPL", "strategy": "nope"}}, 422, "strategy_not_found"),
        ({"headers": auth(), "json": {"symbol": "aapl", "strategy": "rsi"}}, 422, "invalid_request"),
        ({"headers": auth(), "json": {"symbol": "AAPL", "bars_limit": 5}}, 422, "invalid_request"),
    ],
)
def test_errors_use_envelope(client: TestClient, kwargs: dict[str, object], status: int, code: str) -> None:
    kwargs.setdefault("json", {"symbol": "AAPL", "strategy": "rsi"})
    assert_envelope(client.post(GEN, **kwargs), status, code)  # type: ignore[arg-type]


def test_unauthorized_keeps_www_authenticate(client: TestClient) -> None:
    r = client.post(GEN, json={"symbol": "AAPL"})
    assert r.headers["www-authenticate"] == "Bearer"


def test_validation_detail_names_the_field(client: TestClient) -> None:
    r = client.post(GEN, headers=auth(), json={"symbol": "AAPL", "strategy": "rsi", "bars_limit": 5})
    assert r.json()["detail"].startswith("bars_limit:")


def test_market_data_error_does_not_leak_upstream_message(fake: FakeMarketData, client: TestClient) -> None:
    fake.error = MarketDataUnavailableError(symbol="AAPL", reason="alpaca 403: key PKSECRET forbidden")
    r = client.post(GEN, headers=auth(), json={"symbol": "AAPL", "strategy": "rsi"})
    body = assert_envelope(r, 503, "market_data_unavailable")
    for leak in ("alpaca", "PKSECRET", "forbidden"):
        assert leak not in r.text, body


def test_unhandled_exception_is_500_envelope(fake: FakeMarketData, client: TestClient) -> None:
    fake.error = RuntimeError("secret internal state")
    r = client.post(GEN, headers=auth(), json={"symbol": "AAPL", "strategy": "rsi"})
    assert_envelope(r, 500, "internal_error")
    assert "secret" not in r.text


def test_unknown_route_is_404_envelope(client: TestClient) -> None:
    assert_envelope(client.get("/api/v1/nope", headers=auth()), 404, "not_found")


def test_request_id_echoed_when_safe(client: TestClient) -> None:
    r = client.get("/health/live", headers={"X-Request-ID": "bot-engine.req_42"})
    assert r.headers["x-request-id"] == "bot-engine.req_42"


@pytest.mark.parametrize("bad", ["has spaces", "a" * 65, '"><script>', ""])
def test_unsafe_request_id_replaced(client: TestClient, bad: str) -> None:
    r = client.get("/health/live", headers={"X-Request-ID": bad})
    got = r.headers["x-request-id"]
    assert got and got != bad and " " not in got and "<" not in got


def test_request_id_bound_to_audit_log(client: TestClient, capsys: pytest.CaptureFixture[str]) -> None:
    client.get("/api/v1/signals/strategies", headers={**auth(), "X-Request-ID": "corr-777"})
    out = capsys.readouterr().out
    audit = [line for line in out.splitlines() if '"audit.request"' in line]
    assert audit and all('"request_id": "corr-777"' in line for line in audit)
