"""Shared pytest fixtures and helpers for strategy tests."""

from __future__ import annotations

import numpy as np
import pandas as pd
import pytest


def make_bars(n: int = 50, base_price: float = 100.0, seed: int = 42) -> pd.DataFrame:
    """Generate a neutral OHLCV DataFrame with random-walk prices."""
    rng = np.random.default_rng(seed)
    returns = rng.normal(0, 0.005, size=n)
    close = base_price * np.cumprod(1 + returns)
    open_ = np.roll(close, 1)
    open_[0] = base_price
    high = np.maximum(open_, close) * (1 + rng.uniform(0, 0.002, size=n))
    low = np.minimum(open_, close) * (1 - rng.uniform(0, 0.002, size=n))
    volume = rng.integers(100_000, 1_000_000, size=n).astype(float)

    return pd.DataFrame(
        {"open": open_, "high": high, "low": low, "close": close, "volume": volume}
    )


def make_oversold_bars(n: int = 50, period: int = 14) -> pd.DataFrame:
    """Generate bars that produce an RSI < 30 (oversold = BUY signal)."""
    # Steady downtrend with small recoveries — drives RSI low
    close = np.zeros(n)
    close[0] = 100.0
    for i in range(1, n):
        if i % 5 == 0:
            close[i] = close[i - 1] * 1.001  # tiny bounce
        else:
            close[i] = close[i - 1] * 0.985  # sustained drop

    open_ = np.roll(close, 1)
    open_[0] = close[0]
    high = np.maximum(open_, close) * 1.001
    low = np.minimum(open_, close) * 0.999
    volume = np.full(n, 500_000.0)

    return pd.DataFrame(
        {"open": open_, "high": high, "low": low, "close": close, "volume": volume}
    )


def make_overbought_bars(n: int = 50, period: int = 14) -> pd.DataFrame:
    """Generate bars that produce an RSI > 70 (overbought = SELL signal)."""
    close = np.zeros(n)
    close[0] = 100.0
    for i in range(1, n):
        if i % 5 == 0:
            close[i] = close[i - 1] * 0.999  # tiny dip
        else:
            close[i] = close[i - 1] * 1.015  # sustained rally

    open_ = np.roll(close, 1)
    open_[0] = close[0]
    high = np.maximum(open_, close) * 1.001
    low = np.minimum(open_, close) * 0.999
    volume = np.full(n, 500_000.0)

    return pd.DataFrame(
        {"open": open_, "high": high, "low": low, "close": close, "volume": volume}
    )
