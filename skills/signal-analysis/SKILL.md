---
name: signal-analysis
description: Compute RSI and EMA-crossover technical-analysis signals (BUY / SELL / HOLD with strength, confidence and a plain-language reason) from OHLCV price bars for any stock, ETF or crypto symbol. Use when the user asks for a technical read, an RSI or moving-average check, whether an asset looks oversold/overbought, or a signal for a ticker. Read-only and offline - it never places orders, needs no API keys, and makes no network calls.
---

# Signal Analysis

Turns a series of price bars into technical-analysis signals using two classic strategies:

| Strategy | Logic | Defaults |
|---|---|---|
| `rsi` | Wilder RSI. ≤ oversold → BUY, ≥ overbought → SELL, otherwise HOLD. STRONG when beyond the threshold by `--strong-margin`. | period 14, oversold 30, overbought 70, margin 5 |
| `ma_cross` | Fast vs slow EMA on the last two bars. Golden cross → BUY, death cross → SELL, otherwise HOLD. | fast 9, slow 21 |

The math is identical to the open-source `signal-trading` backend this skill is extracted from. See `references/STRATEGIES.md` for formulas and confidence scoring.

## What this skill reads, does and needs

- **Reads:** only the bar file (or stdin) you pass to it.
- **Does:** computes indicators locally and prints JSON to stdout.
- **Does NOT:** place, modify or cancel orders; connect to any broker or exchange; make any network request; read environment variables, credentials or other files; write files.
- **Needs:** Python 3.8+ (standard library only, nothing to install).

## Workflow

1. **Get price bars** for the requested symbol and timeframe using whatever market-data tool is available to you (a stock/crypto data tool, a file the user provides, etc.).
   - Use **closing prices**, ordered oldest → newest (timestamps let the script sort them).
   - Fetch **at least 60 bars**. Minimums: `rsi` needs `period + 1` (15), `ma_cross` needs `slow + 1` (22). More history makes RSI more stable; 100–200 bars is ideal.
   - Prefer completed bars. Note in your answer if the last bar is still forming.
2. **Save the bars** to a temporary `.csv` or `.json` file, or pipe them via stdin. Accepted formats are in `references/INPUT_FORMAT.md`. The minimum is a CSV with a `close` column.
3. **Run the analysis** (paths are relative to this skill's directory):

   ```bash
   python scripts/analyze.py --input bars.csv --symbol AAPL --timeframe 1Day --strategy all
   ```

   Use `python3` if `python` is not Python 3. Useful options: `--strategy rsi|ma_cross|all`, `--period`, `--oversold`, `--overbought`, `--fast`, `--slow`, `--compact`, and `--input -` for stdin.
4. **Read the JSON result** (schema in `references/OUTPUT_FORMAT.md`). Exit code `0` means success, `2` invalid input, `3` not enough bars (the `error.message` says how many are required; fetch more and retry).
5. **Report to the user:**
   - each strategy's direction, strength and confidence;
   - the `reason`, plus key indicator values (RSI, EMAs);
   - the `consensus` (`MIXED` when strategies disagree);
   - any `warnings`;
   - the timeframe and the last bar time.

   **Always include the disclaimer** that this is educational technical analysis, not financial advice.

## Interpretation guidance

- `confidence` is a heuristic score derived from how extreme the indicator is. It is **not** a probability of profit.
- `is_actionable: true` only means the direction is not HOLD. It is not a recommendation to trade.
- RSI and MA-crossover often disagree: RSI is mean-reversion, crossovers are trend-following. Explain the disagreement rather than picking one.
- Signals depend heavily on timeframe. If the user didn't specify one, say which you used (daily is a sensible default).
- Do not present output as a trade instruction, and do not offer to execute trades on the basis of this skill.

## Example

```bash
python scripts/analyze.py --input examples/sample_bars.csv --symbol DEMO --timeframe 1Hour
```

`examples/sample_bars.csv` is **synthetic** data (60 hourly bars ending in a sell-off). Expected result: RSI → `BUY / STRONG` (deeply oversold); MA cross → `HOLD` (no crossover on the last bar); consensus `MIXED`.
