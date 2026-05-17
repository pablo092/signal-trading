"""Symbol value object — an asset ticker identifier."""

from __future__ import annotations

import re

_TICKER_RE = re.compile(r"^[A-Z]{1,10}$")


class Symbol:
    """Immutable asset ticker symbol (e.g. 'AAPL', 'TSLA').

    Examples:
        >>> Symbol(ticker="AAPL")
        Symbol('AAPL')
        >>> Symbol(ticker="aapl")  # auto-uppercased
        Symbol('AAPL')
    """

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
