"""RSI (Relative Strength Index) signal strategy.

Classic momentum oscillator: oversold (<30) → BUY, overbought (>70) → SELL.
Uses pandas-ta for indicator computation.
"""

from __future__ import annotations

import pandas as pd
import pandas_ta as ta  # type: ignore[import]

from src.domain.ports import ISignalStrategy
from src.domain.value_objects import Signal, SignalDirection, SignalStrength, Symbol, Timeframe
from src.domain.value_objects.price import Price


class RSIStrategy(ISignalStrategy):
    """RSI-based signal strategy.

    Args:
        period:         RSI lookback period (default 14).
        oversold:       RSI threshold below which we signal BUY (default 30).
        overbought:     RSI threshold above which we signal SELL (default 70).
        strong_margin:  Additional margin for STRONG signals (default 5).

    Example:
        strategy = RSIStrategy(period=14, oversold=30, overbought=70)
        signal = strategy.analyze(symbol, bars, Timeframe.ONE_HOUR)
    """

    def __init__(
        self,
        period: int = 14,
        oversold: float = 30.0,
        overbought: float = 70.0,
        strong_margin: float = 5.0,
    ) -> None:
        if period < 2:
            raise ValueError(f"RSI period must be >= 2, got {period}")
        if not (0 < oversold < overbought < 100):
            raise ValueError("oversold must be < overbought and both in (0, 100)")

        self._period = period
        self._oversold = oversold
        self._overbought = overbought
        self._strong_margin = strong_margin

    @property
    def name(self) -> str:
        return f"RSIStrategy(period={self._period})"

    @property
    def min_bars_required(self) -> int:
        return self._period + 1

    def analyze(
        self,
        symbol: Symbol,
        bars: pd.DataFrame,
        timeframe: Timeframe,
    ) -> Signal:
        """Compute RSI and emit a directional signal."""
        self.validate_bars(bars)

        rsi_series: pd.Series = ta.rsi(bars["close"], length=self._period)  # type: ignore[assignment]
        current_rsi = float(rsi_series.iloc[-1])
        current_price = float(bars["close"].iloc[-1])

        direction, strength, confidence, reason = self._classify(current_rsi)

        return Signal.create(
            symbol=symbol,
            direction=direction,
            strength=strength,
            price=Price.from_float(current_price),
            strategy_name=self.name,
            confidence=confidence,
            reason=reason,
        )

    def _classify(
        self, rsi: float
    ) -> tuple[SignalDirection, SignalStrength, float, str]:
        """Map RSI value → direction, strength, confidence, reason."""
        strong_oversold = self._oversold - self._strong_margin
        strong_overbought = self._overbought + self._strong_margin

        if rsi <= strong_oversold:
            return (
                SignalDirection.BUY,
                SignalStrength.STRONG,
                self._confidence_from_rsi(rsi, direction="buy"),
                f"RSI={rsi:.1f} — strongly oversold (threshold={strong_oversold})",
            )
        if rsi <= self._oversold:
            return (
                SignalDirection.BUY,
                SignalStrength.MODERATE,
                self._confidence_from_rsi(rsi, direction="buy"),
                f"RSI={rsi:.1f} — oversold (threshold={self._oversold})",
            )
        if rsi >= strong_overbought:
            return (
                SignalDirection.SELL,
                SignalStrength.STRONG,
                self._confidence_from_rsi(rsi, direction="sell"),
                f"RSI={rsi:.1f} — strongly overbought (threshold={strong_overbought})",
            )
        if rsi >= self._overbought:
            return (
                SignalDirection.SELL,
                SignalStrength.MODERATE,
                self._confidence_from_rsi(rsi, direction="sell"),
                f"RSI={rsi:.1f} — overbought (threshold={self._overbought})",
            )
        # Neutral zone
        distance_from_center = abs(rsi - 50) / 50
        return (
            SignalDirection.HOLD,
            SignalStrength.WEAK,
            max(0.1, distance_from_center * 0.3),
            f"RSI={rsi:.1f} — neutral zone",
        )

    def _confidence_from_rsi(self, rsi: float, direction: str) -> float:
        """Map RSI extremity to confidence [0.5, 0.99]."""
        if direction == "buy":
            # More oversold → higher confidence
            normalized = max(0.0, (self._oversold - rsi) / self._oversold)
        else:
            # More overbought → higher confidence
            normalized = max(0.0, (rsi - self._overbought) / (100 - self._overbought))
        return round(min(0.99, 0.5 + normalized * 0.49), 4)
