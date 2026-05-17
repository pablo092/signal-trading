"""Signal value object — the core output of every strategy."""

from __future__ import annotations

from datetime import datetime, timezone
from enum import Enum
from typing import Any
from uuid import UUID, uuid4

from pydantic import Field, field_validator
from pydantic.dataclasses import dataclass

from .price import Price
from .symbol import Symbol


class SignalDirection(str, Enum):
    """Trading signal direction."""

    BUY = "BUY"
    SELL = "SELL"
    HOLD = "HOLD"


class SignalStrength(str, Enum):
    """Confidence level of the signal."""

    STRONG = "STRONG"    # >0.8 confidence
    MODERATE = "MODERATE"  # 0.5–0.8
    WEAK = "WEAK"        # 0.3–0.5


@dataclass(frozen=True)
class Signal:
    """Immutable trading signal produced by a strategy.

    Examples:
        >>> sig = Signal.create(
        ...     symbol=Symbol(ticker="AAPL"),
        ...     direction=SignalDirection.BUY,
        ...     strength=SignalStrength.STRONG,
        ...     price=Price.from_float(150.0),
        ...     strategy_name="RSIStrategy",
        ...     confidence=0.85,
        ...     reason="RSI crossed below 30 — oversold condition",
        ... )
    """

    id: UUID
    symbol: Symbol
    direction: SignalDirection
    strength: SignalStrength
    price: Price
    strategy_name: str
    confidence: float
    reason: str
    generated_at: datetime

    @field_validator("confidence")
    @classmethod
    def validate_confidence(cls, v: float) -> float:
        if not 0.0 <= v <= 1.0:
            raise ValueError(f"Confidence must be in [0, 1], got {v}")
        return round(v, 4)

    @classmethod
    def create(
        cls,
        symbol: Symbol,
        direction: SignalDirection,
        strength: SignalStrength,
        price: Price,
        strategy_name: str,
        confidence: float,
        reason: str,
    ) -> "Signal":
        """Factory method — ensures always-valid Signal creation."""
        return cls(
            id=uuid4(),
            symbol=symbol,
            direction=direction,
            strength=strength,
            price=price,
            strategy_name=strategy_name,
            confidence=confidence,
            reason=reason,
            generated_at=datetime.now(tz=timezone.utc),
        )

    @property
    def is_actionable(self) -> bool:
        """True if signal warrants a trade (not HOLD)."""
        return self.direction != SignalDirection.HOLD

    def __eq__(self, other: Any) -> bool:
        if not isinstance(other, Signal):
            return NotImplemented
        return self.id == other.id

    def __hash__(self) -> int:
        return hash(self.id)

    def __repr__(self) -> str:
        return (
            f"Signal({self.direction.value} {self.symbol} "
            f"@ {self.price} [{self.strength.value}] "
            f"confidence={self.confidence:.2%})"
        )
