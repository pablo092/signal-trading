"""Moving Average Crossover signal strategy.

Golden cross (fast MA crosses above slow MA) → BUY.
Death cross (fast MA crosses below slow MA) → SELL.
"""

from __future__ import annotations

import pandas as pd

from src.domain.ports import ISignalStrategy
from src.domain.value_objects import Signal, SignalDirection, SignalStrength, Symbol, Timeframe
from src.domain.value_objects.price import Price


class MovingAverageCrossStrategy(ISignalStrategy):
    """MA crossover strategy: fast MA vs slow MA.

    Args:
        fast_period: Short MA lookback (default 9).
        slow_period: Long MA lookback (default 21).
        ma_type:     "EMA" (default, ewm span, adjust=False) or "SMA" (rolling mean).

    Example:
        strategy = MovingAverageCrossStrategy(fast_period=9, slow_period=21)
        signal = strategy.analyze(symbol, bars, Timeframe.ONE_HOUR)
    """

    _MA_TYPES = ("EMA", "SMA")

    def __init__(self, fast_period: int = 9, slow_period: int = 21, ma_type: str = "EMA") -> None:
        ma = ma_type.strip().upper() if isinstance(ma_type, str) else ""
        if ma not in self._MA_TYPES:
            raise ValueError(f"ma_type must be one of {self._MA_TYPES}, got {ma_type!r}")
        if fast_period < 1:
            raise ValueError(f"fast_period must be >= 1, got {fast_period}")
        if fast_period >= slow_period:
            raise ValueError(
                f"fast_period ({fast_period}) must be < slow_period ({slow_period})"
            )
        self._fast = fast_period
        self._slow = slow_period
        self._ma_type = ma

    @property
    def name(self) -> str:
        return f"MACrossStrategy({self._ma_type},fast={self._fast},slow={self._slow})"

    def _ma(self, close: pd.Series, period: int) -> pd.Series:
        if self._ma_type == "SMA":
            return close.rolling(window=period).mean()
        return close.ewm(span=period, adjust=False).mean()

    @property
    def min_bars_required(self) -> int:
        return self._slow + 1  # need at least one prior bar for crossover detection

    def analyze(
        self,
        symbol: Symbol,
        bars: pd.DataFrame,
        timeframe: Timeframe,
    ) -> Signal:
        self.validate_bars(bars)

        close = bars["close"]
        fast_ema = self._ma(close, self._fast)
        slow_ema = self._ma(close, self._slow)

        prev_fast, curr_fast = fast_ema.iloc[-2], fast_ema.iloc[-1]
        prev_slow, curr_slow = slow_ema.iloc[-2], slow_ema.iloc[-1]
        current_price = float(close.iloc[-1])

        # Detect crossover
        crossed_above = prev_fast <= prev_slow and curr_fast > curr_slow
        crossed_below = prev_fast >= prev_slow and curr_fast < curr_slow

        if crossed_above:
            gap_pct = (curr_fast - curr_slow) / curr_slow
            confidence = round(min(0.95, 0.65 + gap_pct * 10), 4)
            return Signal.create(
                symbol=symbol,
                direction=SignalDirection.BUY,
                strength=SignalStrength.STRONG if confidence > 0.80 else SignalStrength.MODERATE,
                price=Price.from_float(current_price),
                strategy_name=self.name,
                confidence=confidence,
                reason=f"Golden cross: fast {self._ma_type}({self._fast}) crossed above slow {self._ma_type}({self._slow})",
            )

        if crossed_below:
            gap_pct = (curr_slow - curr_fast) / curr_slow
            confidence = round(min(0.95, 0.65 + gap_pct * 10), 4)
            return Signal.create(
                symbol=symbol,
                direction=SignalDirection.SELL,
                strength=SignalStrength.STRONG if confidence > 0.80 else SignalStrength.MODERATE,
                price=Price.from_float(current_price),
                strategy_name=self.name,
                confidence=confidence,
                reason=f"Death cross: fast {self._ma_type}({self._fast}) crossed below slow {self._ma_type}({self._slow})",
            )

        # No crossover — neutral
        return Signal.create(
            symbol=symbol,
            direction=SignalDirection.HOLD,
            strength=SignalStrength.WEAK,
            price=Price.from_float(current_price),
            strategy_name=self.name,
            confidence=0.1,
            reason=f"No crossover detected (fast={curr_fast:.2f}, slow={curr_slow:.2f})",
        )
