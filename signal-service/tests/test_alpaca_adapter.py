"""AlpacaMarketDataAdapter with a fake alpaca-py client (no network) — BL-08 / TD-13."""

from __future__ import annotations

import asyncio
import threading
import time
from datetime import UTC, datetime, timedelta
from typing import Any

import pandas as pd
import pytest
from alpaca.common.enums import Sort
from alpaca.data.enums import DataFeed
from src.domain.entities.exceptions import MarketDataUnavailableError
from src.domain.value_objects import Symbol, Timeframe
from src.infrastructure.adapters.alpaca_market_data import (
    AlpacaMarketDataAdapter,
    TTLCache,
    compute_start,
)
from src.infrastructure.config import Settings

NOW = datetime(2026, 11, 24, 15, 0, tzinfo=UTC)


def settings(**overrides: Any) -> Settings:
    base: dict[str, Any] = {"ALPACA_API_KEY": "fake", "ALPACA_SECRET_KEY": "fake"}
    base.update(overrides)
    return Settings(_env_file=None, **base)  # type: ignore[call-arg]


def alpaca_frame(ticker: str = "AAPL", n: int = 30, descending: bool = True) -> pd.DataFrame:
    ts = pd.date_range(end=NOW, periods=n, freq="1h", tz="UTC")
    if descending:
        ts = ts[::-1]
    idx = pd.MultiIndex.from_arrays([[ticker] * n, ts], names=["symbol", "timestamp"])
    close = [100.0 + i for i in range(n)]
    return pd.DataFrame(
        {"open": close, "high": close, "low": close, "close": close, "volume": [1.0] * n,
         "trade_count": [1] * n, "vwap": close},
        index=idx,
    )


class FakeResponse:
    def __init__(self, df: pd.DataFrame) -> None:
        self.df = df


class FakeClient:
    def __init__(self, response: Any = None, delay: float = 0.0, error: Exception | None = None) -> None:
        self.response = response if response is not None else FakeResponse(alpaca_frame())
        self.delay = delay
        self.error = error
        self.requests: list[Any] = []
        self.threads: set[int] = set()
        self._lock = threading.Lock()

    def get_stock_bars(self, request: Any) -> Any:
        with self._lock:
            self.requests.append(request)
            self.threads.add(threading.get_ident())
        if self.delay:
            time.sleep(self.delay)
        if self.error:
            raise self.error
        return self.response


def adapter(client: FakeClient, **s: Any) -> AlpacaMarketDataAdapter:
    return AlpacaMarketDataAdapter(settings(**s), client=client, now=lambda: NOW)


SYM = Symbol(ticker="AAPL")


async def test_request_parameters_and_normalization() -> None:
    client = FakeClient()
    df = await adapter(client).get_bars(SYM, Timeframe.ONE_HOUR, limit=30)

    req = client.requests[0]
    assert req.limit == 30
    assert req.sort == Sort.DESC
    assert req.feed == DataFeed.IEX
    # alpaca-py normalizes start to naive UTC; compare the same instant.
    expected = compute_start(Timeframe.ONE_HOUR, 30, NOW)
    assert req.start.replace(tzinfo=UTC) == expected
    assert expected < NOW

    assert list(df.columns) == ["open", "high", "low", "close", "volume"]
    assert df.index.is_monotonic_increasing  # oldest-first even though Alpaca returned desc
    assert len(df) == 30
    assert df["close"].iloc[-1] == 100.0  # the newest bar (first in the desc response)


def test_compute_start_windows() -> None:
    # Small requests get the 10-day floor (weekends/holidays), large ones scale with bar size.
    assert NOW - compute_start(Timeframe.ONE_MIN, 100, NOW) == timedelta(days=10)
    assert NOW - compute_start(Timeframe.ONE_DAY, 100, NOW) == timedelta(days=600)
    assert NOW - compute_start(Timeframe.ONE_DAY, 1000, NOW) == timedelta(days=365 * 15)


async def test_feed_is_configurable() -> None:
    client = FakeClient()
    await adapter(client, ALPACA_DATA_FEED="SIP").get_bars(SYM, Timeframe.ONE_DAY, limit=30)
    assert client.requests[0].feed == DataFeed.SIP


async def test_calls_run_off_the_event_loop_and_concurrently() -> None:
    client = FakeClient(delay=0.3)
    a = adapter(client, MARKET_DATA_CACHE_TTL_SECONDS=0)
    t0 = time.perf_counter()
    await asyncio.gather(*(a.get_bars(SYM, Timeframe.ONE_HOUR, limit=20 + i) for i in range(4)))
    elapsed = time.perf_counter() - t0
    assert len(client.requests) == 4
    assert elapsed < 0.9, f"requests were serialized ({elapsed:.2f}s for 4 x 0.3s)"
    assert threading.get_ident() not in client.threads


async def test_timeout_maps_to_unavailable() -> None:
    client = FakeClient(delay=1.0)
    with pytest.raises(MarketDataUnavailableError, match="timed out"):
        await adapter(client, MARKET_DATA_TIMEOUT_SECONDS=0.1).get_bars(SYM, Timeframe.ONE_HOUR)


async def test_cache_hit_and_isolation() -> None:
    client = FakeClient()
    a = adapter(client)
    first = await a.get_bars(SYM, Timeframe.ONE_HOUR, limit=30)
    first.loc[first.index[0], "close"] = -1.0  # caller mutation must not poison the cache
    second = await a.get_bars(SYM, Timeframe.ONE_HOUR, limit=30)
    assert len(client.requests) == 1
    assert second["close"].iloc[0] != -1.0
    await a.get_bars(SYM, Timeframe.ONE_DAY, limit=30)  # different key → new request
    assert len(client.requests) == 2


def test_ttl_cache_expiry() -> None:
    now = [0.0]
    cache = TTLCache(10, clock=lambda: now[0])
    df = pd.DataFrame({"close": [1.0]})
    cache.set(("k",), df)
    now[0] = 9.9
    assert cache.get(("k",)) is not None
    now[0] = 10.0
    assert cache.get(("k",)) is None
    disabled = TTLCache(0)
    disabled.set(("k",), df)
    assert disabled.get(("k",)) is None


@pytest.mark.parametrize(
    "response,match",
    [
        ({"AAPL": []}, "unexpected raw response"),
        (FakeResponse(pd.DataFrame()), "empty bars"),
        (FakeResponse(alpaca_frame().drop(columns=["volume"])), "missing columns: volume"),
    ],
)
async def test_bad_responses(response: Any, match: str) -> None:
    with pytest.raises(MarketDataUnavailableError, match=match):
        await adapter(FakeClient(response=response)).get_bars(SYM, Timeframe.ONE_HOUR)


async def test_client_errors_are_wrapped() -> None:
    with pytest.raises(MarketDataUnavailableError, match="boom"):
        await adapter(FakeClient(error=RuntimeError("boom"))).get_bars(SYM, Timeframe.ONE_HOUR)


async def test_flat_index_is_accepted() -> None:
    df = alpaca_frame().droplevel(0)
    out = await adapter(FakeClient(response=FakeResponse(df))).get_bars(SYM, Timeframe.ONE_HOUR)
    assert out.index.is_monotonic_increasing


def test_invalid_settings_rejected() -> None:
    from pydantic import ValidationError

    with pytest.raises(ValidationError):
        settings(MARKET_DATA_TIMEOUT_SECONDS=0)
    with pytest.raises(ValueError):
        AlpacaMarketDataAdapter(settings(ALPACA_DATA_FEED="bogus"), client=FakeClient())
