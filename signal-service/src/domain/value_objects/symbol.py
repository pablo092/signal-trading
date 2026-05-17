"""Symbol value object — an asset ticker identifier."""

from __future__ import annotations

import re
from typing import Any

from pydantic import GetCoreSchemaHandler
from pydantic_core import core_schema

_TICKER_RE = re.compile(r"^[A-Z]{1,10}$")


class Symbol:
    """Immutable asset ticker symbol (e.g. 'AAPL', 'TSLA')."""

    __slots__ = ("_ticker",)

    def __init__(self, ticker: str) -> None:
        ticker = ticker.strip().upper()
        if not _TICKER_RE.match(ticker):
            raise ValueError(
                f"Invalid ticker '{ticker}' — must be 1–10 uppercase letters."
            )
        self._ticker = ticker

    @property
    def ticker(self) -> str:
        return self._ticker

    @classmethod
    def __get_pydantic_core_schema__(
        cls, source_type: Any, handler: GetCoreSchemaHandler
    ) -> core_schema.CoreSchema:
        return core_schema.no_info_plain_validator_function(
            lambda v: cls(v) if isinstance(v, str) else v,
            serialization=core_schema.to_string_ser_schema(),
        )

    def __eq__(self, other: object) -> bool:
        if isinstance(other, Symbol):
            return self._ticker == other._ticker
        return NotImplemented

    def __hash__(self) -> int:
        return hash(self._ticker)

    def __repr__(self) -> str:
        return f"Symbol('{self._ticker}')"

    def __str__(self) -> str:
        return self._ticker
