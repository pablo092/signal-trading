"""Timeframe value object — candlestick interval."""

from __future__ import annotations

from enum import Enum


class Timeframe(str, Enum):
    """Supported OHLCV candlestick intervals.

    Values match Alpaca's timeframe strings.
    """

    ONE_MIN = "1Min"
    FIVE_MIN = "5Min"
    FIFTEEN_MIN = "15Min"
    THIRTY_MIN = "30Min"
    ONE_HOUR = "1Hour"
    FOUR_HOUR = "4Hour"
    ONE_DAY = "1Day"

    @property
    def label(self) -> str:
        """Human-readable label."""
        labels = {
            "1Min": "1 minute",
            "5Min": "5 minutes",
            "15Min": "15 minutes",
            "30Min": "30 minutes",
            "1Hour": "1 hour",
            "4Hour": "4 hours",
            "1Day": "1 day",
        }
        return labels[self.value]
