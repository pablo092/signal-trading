"""Price value object — immutable, validated monetary amount."""

from __future__ import annotations

from decimal import Decimal, ROUND_HALF_UP
from typing import Union


class Price:
    """Immutable price value object.

    Stores the value as a Decimal to avoid floating-point rounding errors.

    Examples:
        >>> p = Price.from_float(150.125)
        >>> str(p)
        '150.13'
        >>> p.value
        Decimal('150.13')
    """

    __slots__ = ("_value",)

    def __init__(self, value: Decimal) -> None:
        if value < 0:
            raise ValueError(f"Price cannot be negative, got {value}")
        self._value = value

    @classmethod
    def from_float(cls, value: float, decimals: int = 2) -> "Price":
        """Create a Price from a float, rounding to the given decimal places."""
        quantizer = Decimal(10) ** -decimals
        decimal_value = Decimal(str(value)).quantize(quantizer, rounding=ROUND_HALF_UP)
        return cls(decimal_value)

    @classmethod
    def from_decimal(cls, value: Decimal) -> "Price":
        return cls(value)

    @property
    def value(self) -> Decimal:
        return self._value

    def as_float(self) -> float:
        return float(self._value)

    def __eq__(self, other: object) -> bool:
        if isinstance(other, Price):
            return self._value == other._value
        return NotImplemented

    def __hash__(self) -> int:
        return hash(self._value)

    def __repr__(self) -> str:
        return f"Price({self._value})"

    def __str__(self) -> str:
        return str(self._value)
