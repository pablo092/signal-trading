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
        key = name.strip().lower()
        cls = _REGISTRY.get(key)
        if cls is None:
            raise StrategyNotFoundError(name, available=list(_REGISTRY))
        return cls(**kwargs)

    def available_strategies(self) -> list[str]:
        """Returns the registered strategy names, sorted."""
        return sorted(_REGISTRY)

    @classmethod
    def register(cls, name: str, strategy_cls: type[ISignalStrategy]) -> None:
        """Register a strategy class under a (case-insensitive) name.

        Raises:
            ValueError: If the name is empty or already registered.
            TypeError:  If strategy_cls is not an ISignalStrategy subclass.
        """
        key = name.strip().lower()
        if not key:
            raise ValueError("strategy name must be non-empty")
        if not (isinstance(strategy_cls, type) and issubclass(strategy_cls, ISignalStrategy)):
            raise TypeError("strategy_cls must be a subclass of ISignalStrategy")
        if key in _REGISTRY:
            raise ValueError(f"strategy '{key}' is already registered")
        _REGISTRY[key] = strategy_cls
