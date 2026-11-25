"""BL-16 producer tests: signal-service responses conform to contracts/."""

from __future__ import annotations

import pytest
from fastapi.testclient import TestClient
from src.domain.entities.exceptions import MarketDataUnavailableError

from tests.conftest import make_bars
from tests.contract import assert_conforms, check, example, schema
from tests.test_api import GEN, OTHER, FakeMarketData, auth, client, fake  # noqa: F401  (fixtures)


@pytest.mark.parametrize("strategy", ["rsi", "ma_cross"])
def test_generated_signal_matches_contract(client: TestClient, strategy: str) -> None:  # noqa: F811
    r = client.post(GEN, headers=auth(), json={"symbol": "AAPL", "strategy": strategy})
    assert r.status_code == 200, r.text
    assert_conforms("signal.schema.json", r.json())


def test_hold_signal_matches_contract(client: TestClient, fake: FakeMarketData) -> None:  # noqa: F811
    fake.bars = make_bars(n=120)
    r = client.post(GEN, headers=auth(), json={"symbol": "MSFT", "strategy": "rsi"})
    assert r.status_code == 200, r.text
    assert_conforms("signal.schema.json", r.json())


@pytest.mark.parametrize(
    ("headers", "body", "status"),
    [
        ({}, {"symbol": "AAPL", "strategy": "rsi"}, 401),
        ("other", {"symbol": "AAPL", "strategy": "rsi"}, 403),
        ("agent", {"symbol": "AAPL", "strategy": "nope"}, 422),
        ("agent", {"symbol": "aapl!!", "strategy": "rsi"}, 422),
    ],
)
def test_error_envelopes_match_contract(
    client: TestClient,  # noqa: F811  (pytest fixture imported from test_api)
    headers: object,
    body: dict[str, str],
    status: int,
) -> None:
    h = auth() if headers == "agent" else auth(OTHER) if headers == "other" else {}
    r = client.post(GEN, headers=h, json=body)
    assert r.status_code == status, r.text
    assert_conforms("error.schema.json", r.json())


def test_market_data_error_matches_contract(client: TestClient, fake: FakeMarketData) -> None:  # noqa: F811
    fake.error = MarketDataUnavailableError(symbol="AAPL", reason="upstream down")
    r = client.post(GEN, headers=auth(), json={"symbol": "AAPL", "strategy": "rsi"})
    assert r.status_code == 503
    assert_conforms("error.schema.json", r.json())


def test_404_matches_contract(client: TestClient) -> None:  # noqa: F811
    assert_conforms("error.schema.json", client.get("/api/v1/nope", headers=auth()).json())


def test_validator_rejects_violations() -> None:
    bad = {**example("signal.json"), "direction": "MAYBE", "confidence": 1.5, "extra": True}
    errs = check(schema("signal.schema.json"), bad)
    assert len(errs) == 3, errs
    assert check(schema("error.schema.json"), {"error": "x"})
