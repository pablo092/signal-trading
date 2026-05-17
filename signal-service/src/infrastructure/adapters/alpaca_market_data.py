"""Alpaca Markets adapter — implements IMarketDataRepository.

Uses alpaca-py (alpaca.data) to fetch OHLCV bars from Alpaca's
historical data API. Works in both paper and live modes.
"""

from __future__ import annotations

import pandas as pd
import structlog
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
    Timeframe.ONE_MIN:      AlpacaTimeFrame(1, TimeFrameUnit.Minute),
    Timeframe.FIVE_MIN:     AlpacaTimeFrame(5, TimeFrameUnit.Minute),
    Timeframe.FIFTEEN_MIN:  AlpacaTimeFrame(15, TimeFrameUnit.Minute),
    Timeframe.THIRTY_MIN:   AlpacaTimeFrame(30, TimeFrameUnit.Minute),
    Timeframe.ONE_HOUR:     AlpacaTimeFrame(1, TimeFrameUnit.Hour),
    Timeframe.FOUR_HOUR:    AlpacaTimeFrame(4, TimeFrameUnit.Hour),
    Timeframe.ONE_DAY:      AlpacaTimeFrame(1, TimeFrameUnit.Day),
}


class AlpacaMarketDataAdapter(IMarketDataRepository):
    """Fetches OHLCV bars from Alpaca's historical market data API."""

    def __init__(self, settings: Settings) -> None:
        self._client = StockHistoricalDataClient(
            api_key=settings.alpaca_api_key,
            secret_key=settings.alpaca_secret_key,
        )
        self._log = logger.bind(adapter="alpaca_market_data")

    async def get_bars(
        self,
        symbol: Symbol,
        timeframe: Timeframe,
        limit: int = 100,
    ) -> pd.DataFrame:
        """Fetch OHLCV bars from Alpaca.

        Returns a DataFrame sorted oldest-first with columns:
        open, high, low, close, volume.
        """
        alpaca_tf = _TIMEFRAME_MAP[timeframe]
        self._log.info(
            "market_data.fetching",
            symbol=str(symbol),
            timeframe=timeframe.value,
            limit=limit,
        )

        try:
            request = StockBarsRequest(
                symbol_or_symbols=symbol.ticker,
                timeframe=alpaca_tf,
                limit=limit,
            )
            bars_response = self._client.get_stock_bars(request)
            df = bars_response.df

            if df.empty:
                raise MarketDataUnavailableError(
                    symbol=str(symbol),
                    reason="Alpaca returned empty bars response",
                )

            # Flatten multi-index if present (symbol, timestamp) → timestamp
            if isinstance(df.index, pd.MultiIndex):
                df = df.xs(symbol.ticker, level=0)

            # Normalise column names to lowercase
            df.columns = [c.lower() for c in df.columns]

            # Keep only OHLCV
            df = df[["open", "high", "low", "close", "volume"]].copy()
            df = df.sort_index()

            self._log.info("market_data.fetched", symbol=str(symbol), bars=len(df))
            return df

        except MarketDataUnavailableError:
            raise
        except Exception as exc:
            raise MarketDataUnavailableError(
                symbol=str(symbol),
                reason=str(exc),
            ) from exc
