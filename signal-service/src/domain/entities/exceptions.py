"""Domain exceptions for the signal service."""


class InsufficientDataError(ValueError):
    """Raised when a strategy requires more bars than were fetched."""

    def __init__(self, required: int, got: int) -> None:
        super().__init__(
            f"Strategy requires at least {required} bars, got {got}."
        )
        self.required = required
        self.got = got


class StrategyNotFoundError(KeyError):
    """Raised when a strategy name is not registered in the factory."""

    def __init__(self, name: str) -> None:
        super().__init__(f"Strategy '{name}' is not registered.")
        self.name = name


class MarketDataUnavailableError(RuntimeError):
    """Raised when the market data provider cannot return data."""

    def __init__(self, symbol: str, reason: str) -> None:
        super().__init__(f"Market data unavailable for {symbol}: {reason}")
        self.symbol = symbol
        self.reason = reason
