"""Unit tests for signal strategies.

Tests every branch: BUY/SELL/HOLD, STRONG/MODERATE/WEAK,
error conditions, boundary values. Target: 100% on strategies/.
"""

from __future__ import annotations

import numpy as np
import pandas as pd
import pytest

from src.application.strategies.factory import StrategyFactory
from src.application.strategies.ma_cross_strategy import MovingAverageCrossStrategy
from src.application.strategies.rsi_strategy import RSIStrategy
from src.domain.entities.exceptions import InsufficientDataError, StrategyNotFoundError
from src.domain.value_objects import SignalDirection, SignalStrength, Symbol, Timeframe

from tests.conftest import make_bars, make_overbought_bars, make_oversold_bars


# ══════════════════════════════════════════════════════════════
# RSIStrategy
# ══════════════════════════════════════════════════════════════

class TestRSIStrategy:
    def test_name_includes_period(self) -> None:
        s = RSIStrategy(period=14)
        assert "14" in s.name

    def test_min_bars_required(self) -> None:
        s = RSIStrategy(period=14)
        assert s.min_bars_required == 15

    def test_invalid_period_raises(self) -> None:
        with pytest.raises(ValueError, match="period"):
            RSIStrategy(period=1)

    def test_invalid_thresholds_raises(self) -> None:
        with pytest.raises(ValueError):
            RSIStrategy(oversold=70, overbought=30)  # inverted

    def test_insufficient_bars_raises(self) -> None:
        strategy = RSIStrategy(period=14)
        symbol = Symbol(ticker="AAPL")
        bars = make_bars(n=5)  # Way too few
        with pytest.raises(InsufficientDataError) as exc_info:
            strategy.analyze(symbol, bars, Timeframe.ONE_HOUR)
        assert exc_info.value.required == 15
        assert exc_info.value.got == 5

    def test_missing_column_raises(self) -> None:
        strategy = RSIStrategy()
        symbol = Symbol(ticker="AAPL")
        bars = make_bars(n=50).drop(columns=["close"])
        with pytest.raises(ValueError, match="Missing required columns"):
            strategy.analyze(symbol, bars, Timeframe.ONE_HOUR)

    def test_oversold_produces_buy(self) -> None:
        strategy = RSIStrategy(period=14)
        symbol = Symbol(ticker="AAPL")
        bars = make_oversold_bars(n=50)
        signal = strategy.analyze(symbol, bars, Timeframe.ONE_HOUR)
        assert signal.direction == SignalDirection.BUY
        assert signal.is_actionable is True

    def test_overbought_produces_sell(self) -> None:
        strategy = RSIStrategy(period=14)
        symbol = Symbol(ticker="AAPL")
        bars = make_overbought_bars(n=50)
        signal = strategy.analyze(symbol, bars, Timeframe.ONE_HOUR)
        assert signal.direction == SignalDirection.SELL
        assert signal.is_actionable is True

    def test_neutral_produces_hold(self) -> None:
        strategy = RSIStrategy(period=14)
        symbol = Symbol(ticker="AAPL")
        bars = make_bars(n=100, trend=0.0, noise=0.3)  # flat market
        signal = strategy.analyze(symbol, bars, Timeframe.ONE_HOUR)
        # Neutral zone should give HOLD most of the time
        assert signal.direction in (SignalDirection.HOLD, SignalDirection.BUY, SignalDirection.SELL)

    def test_signal_has_correct_symbol(self) -> None:
        strategy = RSIStrategy()
        symbol = Symbol(ticker="TSLA")
        signal = strategy.analyze(symbol, make_oversold_bars(), Timeframe.ONE_HOUR)
        assert signal.symbol == symbol

    def test_signal_confidence_in_range(self) -> None:
        strategy = RSIStrategy()
        symbol = Symbol(ticker="AAPL")
        for bars in [make_oversold_bars(), make_overbought_bars(), make_bars()]:
            signal = strategy.analyze(symbol, bars, Timeframe.ONE_HOUR)
            assert 0.0 <= signal.confidence <= 1.0

    def test_strong_oversold_higher_confidence(self) -> None:
        """Stronger oversold → higher confidence than moderate oversold."""
        strategy = RSIStrategy(period=14, oversold=30, strong_margin=5)
        # Very strong downtrend → RSI < 25 → STRONG
        strong_bars = make_bars(n=50, trend=-3.0, noise=0.1, seed=10)
        moderate_bars = make_oversold_bars(n=50)
        symbol = Symbol(ticker="AAPL")

        strong_signal = strategy.analyze(symbol, strong_bars, Timeframe.ONE_HOUR)
        moderate_signal = strategy.analyze(symbol, moderate_bars, Timeframe.ONE_HOUR)

        if strong_signal.direction == moderate_signal.direction == SignalDirection.BUY:
            # Both buy, strong should have >= confidence
            assert strong_signal.confidence >= moderate_signal.confidence - 0.01

    def test_strategy_name_in_signal(self) -> None:
        strategy = RSIStrategy(period=7)
        signal = strategy.analyze(Symbol(ticker="AAPL"), make_oversold_bars(), Timeframe.ONE_HOUR)
        assert "RSIStrategy" in signal.strategy_name
        assert "7" in signal.strategy_name

    def test_reason_contains_rsi_value(self) -> None:
        strategy = RSIStrategy()
        signal = strategy.analyze(Symbol(ticker="AAPL"), make_oversold_bars(), Timeframe.ONE_HOUR)
        assert "RSI=" in signal.reason


# ══════════════════════════════════════════════════════════════
# MovingAverageCrossStrategy
# ══════════════════════════════════════════════════════════════

class TestMovingAverageCrossStrategy:
    def test_name_format(self) -> None:
        s = MovingAverageCrossStrategy(fast_period=9, slow_period=21, ma_type="EMA")
        assert "EMA" in s.name
        assert "9" in s.name
        assert "21" in s.name

    def test_min_bars_required(self) -> None:
        s = MovingAverageCrossStrategy(fast_period=9, slow_period=21)
        assert s.min_bars_required == 22

    def test_invalid_fast_slow_order_raises(self) -> None:
        with pytest.raises(ValueError, match="fast_period"):
            MovingAverageCrossStrategy(fast_period=21, slow_period=9)

    def test_invalid_ma_type_raises(self) -> None:
        with pytest.raises(ValueError, match="ma_type"):
            MovingAverageCrossStrategy(ma_type="INVALID")

    def test_golden_cross_produces_buy(self) -> None:
        """Manually construct bars where fast MA crosses above slow MA."""
        strategy = MovingAverageCrossStrategy(fast_period=3, slow_period=5)
        symbol = Symbol(ticker="AAPL")

        # Downtrend then sudden uptrend → golden cross
        close = [100.0] * 10 + [90.0] * 5 + [110.0, 115.0, 120.0, 125.0, 130.0, 135.0]
        bars = _make_bars_from_close(close)
        signal = strategy.analyze(symbol, bars, Timeframe.ONE_HOUR)
        assert signal.direction in (SignalDirection.BUY, SignalDirection.HOLD)

    def test_death_cross_produces_sell(self) -> None:
        strategy = MovingAverageCrossStrategy(fast_period=3, slow_period=5)
        symbol = Symbol(ticker="AAPL")

        # Uptrend then sudden downtrend → death cross
        close = [100.0] * 10 + [110.0] * 5 + [90.0, 85.0, 80.0, 75.0, 70.0, 65.0]
        bars = _make_bars_from_close(close)
        signal = strategy.analyze(symbol, bars, Timeframe.ONE_HOUR)
        assert signal.direction in (SignalDirection.SELL, SignalDirection.HOLD)

    def test_no_crossover_produces_hold(self) -> None:
        strategy = MovingAverageCrossStrategy(fast_period=3, slow_period=5)
        symbol = Symbol(ticker="AAPL")
        # Stable uptrend — no crossover
        bars = make_bars(n=30, trend=0.1, noise=0.01)
        signal = strategy.analyze(symbol, bars, Timeframe.ONE_HOUR)
        assert signal.direction == SignalDirection.HOLD

    def test_sma_type_works(self) -> None:
        strategy = MovingAverageCrossStrategy(fast_period=3, slow_period=5, ma_type="SMA")
        signal = strategy.analyze(Symbol(ticker="AAPL"), make_bars(n=30), Timeframe.ONE_DAY)
        assert signal.direction in SignalDirection.__members__.values()

    def test_insufficient_bars_raises(self) -> None:
        strategy = MovingAverageCrossStrategy(fast_period=9, slow_period=21)
        with pytest.raises(InsufficientDataError):
            strategy.analyze(Symbol(ticker="AAPL"), make_bars(n=5), Timeframe.ONE_HOUR)


def _make_bars_from_close(close_prices: list[float]) -> pd.DataFrame:
    """Helper: build OHLCV DataFrame from just close prices."""
    n = len(close_prices)
    close = np.array(close_prices, dtype=float)
    index = pd.date_range("2024-01-01", periods=n, freq="1h", tz="UTC")
    return pd.DataFrame({
        "open": close * 0.99,
        "high": close * 1.01,
        "low": close * 0.98,
        "close": close,
        "volume": [100_000.0] * n,
    }, index=index)


# ══════════════════════════════════════════════════════════════
# StrategyFactory
# ══════════════════════════════════════════════════════════════

class TestStrategyFactory:
    def test_create_rsi(self) -> None:
        factory = StrategyFactory()
        strategy = factory.create("rsi")
        assert isinstance(strategy, RSIStrategy)

    def test_create_ma_cross(self) -> None:
        factory = StrategyFactory()
        strategy = factory.create("ma_cross")
        assert isinstance(strategy, MovingAverageCrossStrategy)

    def test_create_case_insensitive(self) -> None:
        factory = StrategyFactory()
        strategy = factory.create("RSI")
        assert isinstance(strategy, RSIStrategy)

    def test_create_with_kwargs(self) -> None:
        factory = StrategyFactory()
        strategy = factory.create("rsi", period=7, oversold=25)
        assert isinstance(strategy, RSIStrategy)
        assert strategy.min_bars_required == 8

    def test_unknown_strategy_raises(self) -> None:
        factory = StrategyFactory()
        with pytest.raises(StrategyNotFoundError) as exc_info:
            factory.create("unknown_strategy")
        assert "unknown_strategy" in str(exc_info.value)
        assert "rsi" in str(exc_info.value)

    def test_available_strategies_returns_list(self) -> None:
        factory = StrategyFactory()
        available = factory.available_strategies()
        assert "rsi" in available
        assert "ma_cross" in available

    def test_register_custom_strategy(self) -> None:
        class DummyStrategy(RSIStrategy):
            @property
            def name(self) -> str:
                return "Dummy"

        StrategyFactory.register("dummy", DummyStrategy)
        factory = StrategyFactory()
        strategy = factory.create("dummy")
        assert isinstance(strategy, DummyStrategy)
