"""Technical indicators implemented with the Python standard library only.

The formulas intentionally match signal-service (pandas) so results are
identical to the backend:

* RSI  — Wilder smoothing via pandas ``ewm(com=period-1, min_periods=period)``
         (default ``adjust=True``), applied to gains/losses of close-to-close deltas.
* EMA  — pandas ``ewm(span=n, adjust=False)``.
"""

from __future__ import annotations

import math
from typing import List, Optional, Sequence


def ema(values: Sequence[float], span: int) -> List[float]:
    """Exponential moving average, equivalent to pandas ``ewm(span=span, adjust=False).mean()``."""
    if span < 1:
        raise ValueError(f"span must be >= 1, got {span}")
    if not values:
        return []
    alpha = 2.0 / (span + 1.0)
    out = [float(values[0])]
    for x in values[1:]:
        out.append(alpha * float(x) + (1.0 - alpha) * out[-1])
    return out


def _adjusted_ewm_mean(values: Sequence[float], alpha: float, min_periods: int) -> List[Optional[float]]:
    """pandas ``ewm(alpha=alpha, adjust=True, min_periods=min_periods).mean()`` for a NaN-free series."""
    decay = 1.0 - alpha
    num = 0.0
    den = 0.0
    out: List[Optional[float]] = []
    for i, x in enumerate(values, start=1):
        num = float(x) + decay * num
        den = 1.0 + decay * den
        out.append(num / den if i >= min_periods else None)
    return out


def rsi_wilder(closes: Sequence[float], period: int = 14) -> List[Optional[float]]:
    """Wilder RSI aligned with ``closes`` (index 0 and warm-up values are ``None``).

    Edge case (differs from pandas, which yields NaN): when both average gain and
    average loss are zero (perfectly flat prices) the RSI is reported as 50.0.
    """
    if period < 2:
        raise ValueError(f"RSI period must be >= 2, got {period}")
    if len(closes) < 2:
        return [None] * len(closes)

    deltas = [float(closes[i]) - float(closes[i - 1]) for i in range(1, len(closes))]
    gains = [d if d > 0 else 0.0 for d in deltas]
    losses = [-d if d < 0 else 0.0 for d in deltas]

    alpha = 1.0 / period  # com = period - 1  →  alpha = 1 / (1 + com)
    avg_gain = _adjusted_ewm_mean(gains, alpha, period)
    avg_loss = _adjusted_ewm_mean(losses, alpha, period)

    out: List[Optional[float]] = [None]
    for g, l in zip(avg_gain, avg_loss):
        if g is None or l is None:
            out.append(None)
        elif l == 0.0:
            out.append(50.0 if g == 0.0 else 100.0)
        else:
            out.append(100.0 - 100.0 / (1.0 + g / l))
    return out


def is_finite_positive(x: float) -> bool:
    return isinstance(x, (int, float)) and math.isfinite(x) and x > 0
