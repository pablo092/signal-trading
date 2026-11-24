# Strategies

Both strategies use **close prices only** and look at the **most recent bar**.

## RSI (`rsi`)

**Indicator: Wilder's RSI.**

1. `delta_t = close_t − close_{t−1}`
2. `gain = max(delta, 0)`, `loss = max(−delta, 0)`
3. Average gain and average loss are exponentially weighted with `alpha = 1/period`. This uses the bias-adjusted form, identical to pandas `ewm(com=period−1, min_periods=period)`. The first value is emitted after `period` deltas.
4. `RSI = 100 − 100 / (1 + avg_gain / avg_loss)`. If avg_loss is 0, RSI is 100. If both are 0 (flat prices), RSI is 50.

**Classification** (defaults: oversold 30, overbought 70, strong margin 5):

| RSI | Direction | Strength |
|---|---|---|
| ≤ oversold − margin (25) | BUY | STRONG |
| ≤ oversold (30) | BUY | MODERATE |
| ≥ overbought + margin (75) | SELL | STRONG |
| ≥ overbought (70) | SELL | MODERATE |
| otherwise | HOLD | WEAK |

**Confidence:**
- BUY: `min(0.99, 0.5 + 0.49 × max(0, (oversold − RSI) / oversold))`
- SELL: `min(0.99, 0.5 + 0.49 × max(0, (RSI − overbought) / (100 − overbought)))`
- HOLD: `max(0.1, 0.3 × |RSI − 50| / 50)`

**Minimum bars:** `period + 1`.

## EMA crossover (`ma_cross`)

**Indicator:** exponential moving averages with `alpha = 2/(span+1)`, seeded with the first close. This is identical to pandas `ewm(span=n, adjust=False)`.

**Classification** compares the last two bars:

| Condition | Direction |
|---|---|
| `fast_prev ≤ slow_prev` and `fast_now > slow_now` (golden cross) | BUY |
| `fast_prev ≥ slow_prev` and `fast_now < slow_now` (death cross) | SELL |
| otherwise | HOLD (WEAK, confidence 0.1) |

**Confidence on a cross:** `min(0.95, 0.65 + 10 × |fast_now − slow_now| / slow_now)`. Strength is STRONG when confidence > 0.80, otherwise MODERATE.

**Minimum bars:** `slow + 1`. Because EMAs are seeded with the first close, use well over `slow` bars (≥ 3× slow) for stable values.

## Limitations

- These are textbook indicators with fixed heuristics. They carry no fundamental, volume, news or risk information.
- A crossover is detected only if it happens **on the last bar**. An older crossover reports HOLD.
- Confidence scores are not calibrated probabilities and say nothing about expected returns.
- Results depend on the timeframe and on the data source's adjustments (splits, dividends).

## Provenance

Extracted from `signal-service/src/application/strategies/` of the open-source `signal-trading` project and re-implemented without third-party dependencies. Parity with the pandas implementation is covered by the project's tests. The only differences:
- the flat-price RSI edge case (50 instead of NaN);
- ASCII `-` instead of an em dash in `reason` strings.
