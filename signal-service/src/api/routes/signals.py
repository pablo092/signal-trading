"""Signal generation HTTP routes.

All endpoints are documented via FastAPI's auto-generated OpenAPI/Swagger.
Access Swagger UI at /docs, ReDoc at /redoc.
"""

from __future__ import annotations

import time

import structlog
from fastapi import APIRouter, Depends, HTTPException, status

from src.api.schemas import (
    GenerateSignalRequest,
    SignalResponse,
    StrategyListResponse,
)
from src.application.generate_signal import GenerateSignalUseCase
from src.application.strategies.factory import StrategyFactory
from src.domain.entities.exceptions import (
    InsufficientDataError,
    MarketDataUnavailableError,
    StrategyNotFoundError,
)
from src.domain.value_objects import Symbol, Timeframe
from src.infrastructure.adapters.alpaca_market_data import AlpacaMarketDataAdapter
from src.infrastructure.config import Settings

logger = structlog.get_logger(__name__)
router = APIRouter(prefix="/api/v1/signals", tags=["Signals"])

# Module-level singletons (injected at app startup via app.state)
_settings: Settings | None = None
_factory: StrategyFactory | None = None
_market_data: AlpacaMarketDataAdapter | None = None


def get_settings() -> Settings:
    assert _settings is not None, "Settings not initialised"
    return _settings


def get_factory() -> StrategyFactory:
    assert _factory is not None, "StrategyFactory not initialised"
    return _factory


def get_market_data() -> AlpacaMarketDataAdapter:
    assert _market_data is not None, "MarketDataAdapter not initialised"
    return _market_data


def init_dependencies(settings: Settings) -> None:
    """Called once at application startup."""
    global _settings, _factory, _market_data
    _settings = settings
    _factory = StrategyFactory()
    _market_data = AlpacaMarketDataAdapter(settings)


@router.post(
    "/generate",
    response_model=SignalResponse,
    status_code=status.HTTP_200_OK,
    summary="Generate a trading signal",
    description=(
        "Fetches recent OHLCV bars from Alpaca, applies the selected strategy, "
        "and returns a Signal with direction (BUY/SELL/HOLD), strength, confidence "
        "and a human-readable reason."
    ),
    responses={
        422: {"description": "Validation error (bad symbol, unknown strategy, etc.)"},
        503: {"description": "Market data unavailable"},
    },
)
async def generate_signal(
    body: GenerateSignalRequest,
    factory: StrategyFactory = Depends(get_factory),
    market_data: AlpacaMarketDataAdapter = Depends(get_market_data),
) -> SignalResponse:
    log = logger.bind(symbol=body.symbol, strategy=body.strategy)
    t0 = time.perf_counter()

    try:
        symbol = Symbol(ticker=body.symbol)
        timeframe = Timeframe(body.timeframe.value)
        strategy = factory.create(body.strategy)

        use_case = GenerateSignalUseCase(
            market_data_repo=market_data,
            strategy=strategy,
        )
        signal = await use_case.execute(
            symbol=symbol,
            timeframe=timeframe,
            bars_limit=body.bars_limit,
        )

        latency = time.perf_counter() - t0
        log.info(
            "signal.generated",
            direction=signal.direction.value,
            confidence=signal.confidence,
            latency_ms=round(latency * 1000, 1),
        )

        return SignalResponse(
            id=signal.id,
            symbol=str(signal.symbol),
            direction=signal.direction.value,  # type: ignore[arg-type]
            strength=signal.strength.value,    # type: ignore[arg-type]
            price=signal.price.as_float(),
            confidence=signal.confidence,
            reason=signal.reason,
            strategy_name=signal.strategy_name,
            is_actionable=signal.is_actionable,
            generated_at=signal.generated_at,
        )

    except StrategyNotFoundError as exc:
        raise HTTPException(
            status_code=status.HTTP_422_UNPROCESSABLE_ENTITY,
            detail=str(exc),
        ) from exc

    except InsufficientDataError as exc:
        raise HTTPException(
            status_code=status.HTTP_422_UNPROCESSABLE_ENTITY,
            detail=str(exc),
        ) from exc

    except MarketDataUnavailableError as exc:
        log.error("market_data.unavailable", error=str(exc))
        raise HTTPException(
            status_code=status.HTTP_503_SERVICE_UNAVAILABLE,
            detail=str(exc),
        ) from exc

    except ValueError as exc:
        raise HTTPException(
            status_code=status.HTTP_422_UNPROCESSABLE_ENTITY,
            detail=str(exc),
        ) from exc


@router.get(
    "/strategies",
    response_model=StrategyListResponse,
    summary="List available strategies",
    description="Returns the names of all registered signal strategies.",
)
def list_strategies(
    factory: StrategyFactory = Depends(get_factory),
) -> StrategyListResponse:
    return StrategyListResponse(strategies=factory.available_strategies)
