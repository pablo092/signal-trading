"""Alpaca Markets adapter — implements IMarketDataRepository (read-only market data).

- The blocking alpaca-py call runs in a worker thread, so concurrent requests are
  not serialized on the event loop (TD-13).
- Every call has a timeout; a timeout surfaces as MarketDataUnavailableError (HTTP 503).
- A computed ``start`` window plus ``sort=desc`` and ``limit`` returns the most
  recent ``limit`` bars (without ``start`` Alpaca only returns the current day).
- Results are cached for a short TTL per (symbol, timeframe, limit).
"""

from __future__ import annotations

import asyncio
import time
from collections.abc import Callable
from datetime import UTC, datetime, timedelta
from typing import Any

import pandas as pd
import structlog
from alpaca.common.enums import Sort
from alpaca.data.enums import DataFeed
from alpaca.data.historical import StockHistoricalDataClient
from alpaca.data.requests import StockBarsRequest
from alpaca.data.timeframe import TimeFrame as AlpacaTimeFrame
from alpaca.data.timeframe import TimeFrameUnit

from src.domain.entities.exceptions import MarketDataUnavailableError
from src.domain.ports import IMarketDataRepository
from src.domain.value_objects import Symbol, Timeframe
from src.infrastructure.config import Settings

logger = structlog.get_logger(__name__)

_TIMEFRAME_MAP: dict[Timeframe, AlpacaTimeFrame] = {
    Timeframe.ONE_MIN: AlpacaTimeFrame(1, TimeFrameUnit.Minute),
    Timeframe.FIVE_MIN: AlpacaTimeFrame(5, TimeFrameUnit.Minute),
    Timeframe.FIFTEEN_MIN: AlpacaTimeFrame(15, TimeFrameUnit.Minute),
    Timeframe.THIRTY_MIN: AlpacaTimeFrame(30, TimeFrameUnit.Minute),
    Timeframe.ONE_HOUR: AlpacaTimeFrame(1, TimeFrameUnit.Hour),
    Timeframe.FOUR_HOUR: AlpacaTimeFrame(4, TimeFrameUnit.Hour),
    Timeframe.ONE_DAY: AlpacaTimeFrame(1, TimeFrameUnit.Day),
}

_TIMEFRAME_DELTA: dict[Timeframe, timedelta] = {
    Timeframe.ONE_MIN: timedelta(minutes=1),
    Timeframe.FIVE_MIN: timedelta(minutes=5),
    Timeframe.FIFTEEN_MIN: timedelta(minutes=15),
    Timeframe.THIRTY_MIN: timedelta(minutes=30),
    Timeframe.ONE_HOUR: timedelta(hours=1),
    Timeframe.FOUR_HOUR: timedelta(hours=4),
    Timeframe.ONE_DAY: timedelta(days=1),
}

_OHLCV = ["open", "high", "low", "close", "volume"]

# Markets trade ~6.5 of 24 hours on ~5 of 7 days, so calendar time per bar is
# several times the bar size. With sort=desc + limit, an over-wide window costs nothing.
_WINDOW_MULTIPLIER = 6
_MIN_WINDOW = timedelta(days=10)
_MAX_WINDOW = timedelta(days=365 * 15)


def compute_start(timeframe: Timeframe, limit: int, now: datetime) -> datetime:
    """Earliest timestamp to request so that ``limit`` recent bars fit in the window."""
    window = _TIMEFRAME_DELTA[timeframe] * limit * _WINDOW_MULTIPLIER
    window = min(max(window, _MIN_WINDOW), _MAX_WINDOW)
    return now - window


class TTLCache:
    """Tiny in-process TTL cache (per worker). Values are copied in and out."""

    def __init__(self, ttl_seconds: float, clock: Callable[[], float] = time.monotonic) -> None:
        self._ttl = ttl_seconds
        self._clock = clock
        self._data: dict[tuple[Any, ...], tuple[float, pd.DataFrame]] = {}

    def get(self, key: tuple[Any, ...]) -> pd.DataFrame | None:
        if self._ttl <= 0:
            return None
        hit = self._data.get(key)
        if hit is None:
            return None
        expires, df = hit
        if self._clock() >= expires:
            self._data.pop(key, None)
            return None
        return df.copy()

    def set(self, key: tuple[Any, ...], df: pd.DataFrame) -> None:
        if self._ttl <= 0:
            return
        self._data[key] = (self._clock() + self._ttl, df.copy())


class AlpacaMarketDataAdapter(IMarketDataRepository):
    """Fetches OHLCV bars from Alpaca's historical market data API."""

    def __init__(
        self,
        settings: Settings,
        client: Any | None = None,
        now: Callable[[], datetime] = lambda: datetime.now(UTC),
    ) -> None:
        self._client = client or StockHistoricalDataClient(
            api_key=settings.alpaca_api_key,
            secret_key=settings.alpaca_secret_key,
        )
        self._timeout = settings.market_data_timeout_seconds
        self._feed = DataFeed(settings.alpaca_data_feed.lower())
        self._cache = TTLCache(settings.market_data_cache_ttl_seconds)
        self._now = now
        self._log = logger.bind(adapter="alpaca_market_data")

    async def get_bars(
        self,
        symbol: Symbol,
        timeframe: Timeframe,
        limit: int = 100,
    ) -> pd.DataFrame:
        """Return the most recent ``limit`` bars, oldest-first, columns open..volume."""
        key = (symbol.ticker, timeframe.value, limit)
        cached = self._cache.get(key)
        if cached is not None:
            self._log.info("market_data.cache_hit", symbol=str(symbol), timeframe=timeframe.value)
            return cached

        request = StockBarsRequest(
            symbol_or_symbols=symbol.ticker,
            timeframe=_TIMEFRAME_MAP[timeframe],
            start=compute_start(timeframe, limit, self._now()),
            limit=limit,
            sort=Sort.DESC,
            feed=self._feed,
        )
        self._log.info(
            "market_data.fetching", symbol=str(symbol), timeframe=timeframe.value, limit=limit
        )

        try:
            response = await asyncio.wait_for(
                asyncio.to_thread(self._client.get_stock_bars, request),
                timeout=self._timeout,
            )
        except TimeoutError as exc:
            raise MarketDataUnavailableError(
                symbol=str(symbol), reason=f"market data request timed out after {self._timeout}s"
            ) from exc
        except Exception as exc:
            raise MarketDataUnavailableError(symbol=str(symbol), reason=str(exc)) from exc

        df = self._normalize(symbol, response)
        self._cache.set(key, df)
        self._log.info("market_data.fetched", symbol=str(symbol), bars=len(df))
        return df

    @staticmethod
    def _normalize(symbol: Symbol, response: Any) -> pd.DataFrame:
        if isinstance(response, dict) or not hasattr(response, "df"):
            # A raw dict is only returned when the client is built with raw_data=True.
            raise MarketDataUnavailableError(
                symbol=str(symbol), reason="unexpected raw response from Alpaca client"
            )
        df = response.df
        if df.empty:
            raise MarketDataUnavailableError(
                symbol=str(symbol), reason="Alpaca returned empty bars response"
            )
        # Flatten (symbol, timestamp) multi-index → timestamp
        if isinstance(df.index, pd.MultiIndex):
            df = df.xs(symbol.ticker, level=0)
        df.columns = [str(c).lower() for c in df.columns]
        missing = [c for c in _OHLCV if c not in df.columns]
        if missing:
            raise MarketDataUnavailableError(
                symbol=str(symbol), reason=f"response missing columns: {', '.join(missing)}"
            )
        return df[_OHLCV].sort_index().copy()
