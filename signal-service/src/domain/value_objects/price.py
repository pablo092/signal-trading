"""Price value object — immutable, validated monetary amount."""

from __future__ import annotations

from decimal import Decimal, ROUND_HALF_UP
from typing import Any, Union

from pydantic import GetCoreSchemaHandler
from pydantic_core import core_schema


class Price:
    """Immutable price value object backed by Decimal."""

    __slots__ = ("_value",)

    def __init__(self, value: Decimal) -> None:
        if value < 0:
            raise ValueError(f"Price cannot be negative, got {value}")
        self._value = value

    @classmethod
    def from_float(cls, value: float, decimals: int = 2) -> "Price":
        quantizer = Decimal(10) ** -decimals
        decimal_value = Decimal(str(value)).quantize(quantizer, rounding=ROUND_HALF_UP)
        return cls(decimal_value)

    @classmethod
    def from_decimal(cls, value: Decimal) -> "Price":
        return cls(value)

    @classmethod
    def __get_pydantic_core_schema__(
        cls, source_type: Any, handler: GetCoreSchemaHandler
    ) -> core_schema.CoreSchema:
        def validate(v: Any) -> "Price":
            if isinstance(v, cls):
                return v
            if isinstance(v, (int, float)):
                return cls.from_float(float(v))
            if isinstance(v, Decimal):
                return cls.from_decimal(v)
            if isinstance(v, str):
                return cls.from_decimal(Decimal(v))
            raise ValueError(f"Cannot create Price from {type(v)}")

        return core_schema.no_info_plain_validator_function(
            validate,
            serialization=core_schema.plain_serializer_function_ser_schema(
                lambda p: float(p._value),
                info_arg=False,
            ),
        )

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
