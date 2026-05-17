"""StrategyFactory — creates signal strategies by name.

Usage:
    factory = StrategyFactory()
    strategy = factory.create("rsi", period=14)
    strategy = factory.create("ma_cross", fast_period=9, slow_period=21)
"""

from __future__ import annotations

from typing import Any

from src.domain.entities.exceptions import StrategyNotFoundError
from src.domain.ports import ISignalStrategy

from .ma_cross_strategy import MovingAverageCrossStrategy
from .rsi_strategy import RSIStrategy

_REGISTRY: dict[str, type[ISignalStrategy]] = {
    "rsi": RSIStrategy,
    "ma_cross": MovingAverageCrossStrategy,
}


class StrategyFactory:
    """Creates and returns configured strategy instances by name."""

    def create(self, name: str, **kwargs: Any) -> ISignalStrategy:
        """Instantiate a strategy by its registered name.

        Args:
            name:   Strategy identifier (e.g. 'rsi', 'ma_cross').
            **kwargs: Constructor arguments forwarded to the strategy class.

        Returns:
            A configured ISignalStrategy instance.

        Raises:
            StrategyNotFoundError: If the name is not registered.
        """
        cls = _REGISTRY.get(name)
        if cls is None:
            raise StrategyNotFoundError(name)
        return cls(**kwargs)

    @property
    def available_strategies(self) -> list[str]:
        """Returns the list of registered strategy names."""
        return list(_REGISTRY.keys())
