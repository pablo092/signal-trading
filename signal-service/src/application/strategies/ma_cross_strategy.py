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
    """MA crossover strategy: fast EMA vs slow EMA.

    Args:
        fast_period: Short EMA lookback (default 9).
        slow_period: Long EMA lookback (default 21).

    Example:
        strategy = MovingAverageCrossStrategy(fast_period=9, slow_period=21)
        signal = strategy.analyze(symbol, bars, Timeframe.ONE_HOUR)
    """

    def __init__(self, fast_period: int = 9, slow_period: int = 21) -> None:
        if fast_period >= slow_period:
            raise ValueError(
                f"fast_period ({fast_period}) must be < slow_period ({slow_period})"
            )
        self._fast = fast_period
        self._slow = slow_period

    @property
    def name(self) -> str:
        return f"MACrossStrategy(fast={self._fast},slow={self._slow})"

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
        fast_ema = close.ewm(span=self._fast, adjust=False).mean()
        slow_ema = close.ewm(span=self._slow, adjust=False).mean()

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
                reason=f"Golden cross: fast EMA({self._fast}) crossed above slow EMA({self._slow})",
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
                reason=f"Death cross: fast EMA({self._fast}) crossed below slow EMA({self._slow})",
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
