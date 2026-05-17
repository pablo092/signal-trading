"""Pydantic request/response schemas for the Signal Service API.

These are the public contract — keep them stable and versioned.
"""

from __future__ import annotations

from datetime import datetime
from enum import Enum
from uuid import UUID

from pydantic import BaseModel, Field


# ── Enums ────────────────────────────────────────────────────────────────────

class DirectionSchema(str, Enum):
    BUY = "BUY"
    SELL = "SELL"
    HOLD = "HOLD"


class StrengthSchema(str, Enum):
    STRONG = "STRONG"
    MODERATE = "MODERATE"
    WEAK = "WEAK"


class TimeframeSchema(str, Enum):
    ONE_MIN     = "1Min"
    FIVE_MIN    = "5Min"
    FIFTEEN_MIN = "15Min"
    THIRTY_MIN  = "30Min"
    ONE_HOUR    = "1Hour"
    FOUR_HOUR   = "4Hour"
    ONE_DAY     = "1Day"


# ── Requests ─────────────────────────────────────────────────────────────────

class GenerateSignalRequest(BaseModel):
    """Request body to generate a trading signal."""

    symbol: str = Field(
        ...,
        examples=["AAPL"],
        description="Asset ticker symbol (1–10 uppercase letters).",
        pattern=r"^[A-Z]{1,10}$",
    )
    strategy: str = Field(
        ...,
        examples=["rsi"],
        description="Strategy name. Available: rsi, ma_cross.",
    )
    timeframe: TimeframeSchema = Field(
        TimeframeSchema.ONE_HOUR,
        description="Candlestick interval for market data.",
    )
    bars_limit: int = Field(
        100,
        ge=20,
        le=1000,
        description="Number of historical bars to fetch (min 20, max 1000).",
    )


# ── Responses ────────────────────────────────────────────────────────────────

class SignalResponse(BaseModel):
    """A generated trading signal."""

    id: UUID = Field(..., description="Unique signal identifier.")
    symbol: str = Field(..., examples=["AAPL"])
    direction: DirectionSchema
    strength: StrengthSchema
    price: float = Field(..., description="Market price at signal generation time.")
    confidence: float = Field(..., ge=0.0, le=1.0, description="Signal confidence (0–1).")
    reason: str = Field(..., description="Human-readable explanation.")
    strategy_name: str = Field(..., examples=["RSIStrategy(period=14)"])
    is_actionable: bool = Field(..., description="False when direction is HOLD.")
    generated_at: datetime

    model_config = {"from_attributes": True}


class StrategyListResponse(BaseModel):
    strategies: list[str] = Field(..., examples=[["rsi", "ma_cross"]])


class HealthResponse(BaseModel):
    status: str
    service: str = "signal-service"
    environment: str
    paper_trading: bool


class ErrorResponse(BaseModel):
    detail: str
    code: str = "INTERNAL_ERROR"
