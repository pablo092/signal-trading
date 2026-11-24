# Output format

The script prints one JSON object to stdout (pretty-printed; single line with `--compact`). Output is ASCII-only (non-ASCII is `\u`-escaped).

## Success (exit code 0)

```json
{
  "tool": "signal-analysis",
  "version": "1.0.0",
  "symbol": "DEMO",
  "timeframe": "1Hour",
  "disclaimer": "Educational technical-analysis output only. Not financial advice. ...",
  "bars_used": 60,
  "last_bar_time": "2024-01-05T01:30:00+00:00",
  "generated_at": "2026-11-24T15:41:16+00:00",
  "signals": [
    {
      "strategy": "RSIStrategy(period=14)",
      "direction": "BUY",
      "strength": "STRONG",
      "confidence": 0.9197,
      "is_actionable": true,
      "price": 78.95,
      "reason": "RSI=4.3 - strongly oversold (threshold=25.0)",
      "indicators": {"rsi": 4.3057, "period": 14, "oversold": 30.0, "overbought": 70.0}
    },
    {
      "strategy": "MACrossStrategy(fast=9,slow=21)",
      "direction": "HOLD",
      "strength": "WEAK",
      "confidence": 0.1,
      "is_actionable": false,
      "price": 78.95,
      "reason": "No crossover detected (fast=83.33, slow=88.85)",
      "indicators": {"fast_ema": 83.3296, "slow_ema": 88.8518, "fast_period": 9, "slow_period": 21}
    }
  ],
  "consensus": "MIXED",
  "warnings": []
}
```

| Field | Meaning |
|---|---|
| `symbol`, `timeframe` | Labels echoed from the arguments (`symbol` upper-cased). Not validated against any market. |
| `bars_used` | Number of bars analyzed |
| `last_bar_time` | Time of the newest bar, or `null` if the input had no usable timestamps |
| `signals[].direction` | `BUY`, `SELL` or `HOLD` |
| `signals[].strength` | `STRONG`, `MODERATE` or `WEAK` (HOLD is always WEAK) |
| `signals[].confidence` | Heuristic score 0–1 (see STRATEGIES.md); **not a probability** |
| `signals[].is_actionable` | `true` when direction ≠ HOLD |
| `signals[].price` | Last close, rounded to 2 decimals |
| `consensus` | The common direction if all signals agree, otherwise `MIXED` |
| `warnings` | Data-quality notes (ordering, missing timestamps, duplicates) |

## Errors

| Exit code | `error.code` | Typical cause | What to do |
|---|---|---|---|
| 2 | `invalid_input` | unreadable file, no `close` column, non-numeric/negative price, bad parameters | Fix the data or arguments |
| 3 | `insufficient_data` | fewer bars than the strategy needs | Fetch more history and retry |

```json
{"tool": "signal-analysis", "version": "1.0.0", "symbol": "AAPL", "timeframe": null,
 "disclaimer": "...", "error": {"code": "insufficient_data", "message": "RSI(14) requires at least 15 bars, got 3"}}
```

Invalid command-line flags are reported by argparse on stderr, also with exit code 2.
