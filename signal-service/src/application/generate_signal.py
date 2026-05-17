"""GenerateSignalUseCase — application layer orchestrator.

Follows the Use Case pattern: one class, one responsibility.
Coordinates ports (IMarketDataRepository, ISignalStrategy) to
produce a Signal. Has no knowledge of HTTP, databases, or Alpaca.
"""

from __future__ import annotations

import structlog

from src.domain.ports import IMarketDataRepository, ISignalStrategy
from src.domain.value_objects import Signal, Symbol, Timeframe

logger = structlog.get_logger(__name__)


class GenerateSignalUseCase:
    """Orchestrate market data fetch → strategy analysis → Signal output.

    Dependencies are injected via constructor (Dependency Inversion).
    This makes the use case fully testable with mocks.

    Args:
        market_data_repo: Port to fetch OHLCV bars.
        strategy:         The signal strategy to apply.

    Example:
        use_case = GenerateSignalUseCase(
            market_data_repo=AlpacaAdapter(settings),
            strategy=RSIStrategy(period=14),
        )
        signal = await use_case.execute(
            symbol=Symbol(ticker="AAPL"),
            timeframe=Timeframe.ONE_HOUR,
        )
    """

    def __init__(
        self,
        market_data_repo: IMarketDataRepository,
        strategy: ISignalStrategy,
    ) -> None:
        self._repo = market_data_repo
        self._strategy = strategy

    async def execute(
        self,
        symbol: Symbol,
        timeframe: Timeframe,
        bars_limit: int = 100,
    ) -> Signal:
        """Run the full signal generation pipeline.

        Args:
            symbol:     Asset to analyze.
            timeframe:  Candlestick interval.
            bars_limit: How many bars to fetch (must exceed strategy minimum).

        Returns:
            A Signal value object.

        Raises:
            InsufficientDataError:       Not enough bars fetched.
            MarketDataUnavailableError:  Broker/network failure.
        """
        log = logger.bind(
            symbol=str(symbol),
            strategy=self._strategy.name,
            timeframe=timeframe.value,
        )

        log.info("signal_generation.started")

        bars = await self._repo.get_bars(
            symbol=symbol,
            timeframe=timeframe,
            limit=max(bars_limit, self._strategy.min_bars_required + 5),
        )

        log.info("signal_generation.bars_fetched", count=len(bars))

        signal = self._strategy.analyze(
            symbol=symbol,
            bars=bars,
            timeframe=timeframe,
        )

        log.info(
            "signal_generation.completed",
            direction=signal.direction.value,
            strength=signal.strength.value,
            confidence=signal.confidence,
            is_actionable=signal.is_actionable,
        )

        return signal
