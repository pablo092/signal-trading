"""Domain ports (interfaces) — inbound and outbound boundaries.

These abstract base classes define the contracts that infrastructure
adapters must implement. The domain never depends on concrete implementations.
"""

from __future__ import annotations

from abc import ABC, abstractmethod

import pandas as pd

from .value_objects import Signal, Symbol, Timeframe


class ISignalStrategy(ABC):
    """Inbound port — strategy that converts market bars into a Signal."""

    @property
    @abstractmethod
    def name(self) -> str:
        """Unique human-readable strategy identifier."""

    @property
    @abstractmethod
    def min_bars_required(self) -> int:
        """Minimum number of OHLCV bars needed to compute the signal."""

    @abstractmethod
    def analyze(
        self,
        symbol: Symbol,
        bars: pd.DataFrame,
        timeframe: Timeframe,
    ) -> Signal:
        """Analyze market data and return a Signal.

        Args:
            symbol:    The asset being analyzed.
            bars:      OHLCV DataFrame with columns: open, high, low, close, volume.
            timeframe: The candlestick interval.

        Returns:
            A Signal value object.

        Raises:
            InsufficientDataError: If len(bars) < min_bars_required.
        """

    def validate_bars(self, bars: pd.DataFrame) -> None:
        """Raises InsufficientDataError if bars is too short."""
        from .entities.exceptions import InsufficientDataError

        if len(bars) < self.min_bars_required:
            raise InsufficientDataError(
                required=self.min_bars_required,
                got=len(bars),
            )


class IMarketDataRepository(ABC):
    """Outbound port — fetches OHLCV market data from a provider."""

    @abstractmethod
    async def get_bars(
        self,
        symbol: Symbol,
        timeframe: Timeframe,
        limit: int = 100,
    ) -> pd.DataFrame:
        """Fetch OHLCV bars.

        Args:
            symbol:    The asset to fetch.
            timeframe: The candlestick interval.
            limit:     Maximum number of bars to return.

        Returns:
            DataFrame with columns: open, high, low, close, volume.
            Sorted oldest-first.

        Raises:
            MarketDataUnavailableError: If the provider cannot return data.
        """
