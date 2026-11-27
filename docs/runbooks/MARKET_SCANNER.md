# Runbook: market scanner

The scanner watches the market so the owner does not have to. Every 5 minutes during
regular US market hours it asks each allowed strategy about each allowed symbol. When a
signal is actionable (BUY or SELL, confidence at least the risk minimum), it proposes an
order intent. The intent goes through every risk check and, if they pass, waits in
`pending_approval` for the owner.

**The scanner only proposes.** It has no token and cannot approve; every order still needs
the owner's approval in the app (ADR-005 §5.3, ADR-006). Paper account only.

## What the owner sees

| Where | What |
|---|---|
| App, **Opportunities** tab (home of a local build) | one card per pending intent: *BUY 3 AAPL*, the amount, the strategy's reason (e.g. "RSI=27.9, oversold"), the risk checks, a countdown, Approve / Reject |
| Browser notification | while the app is open, after *Turn on* on the Opportunities tab |
| Email | Grafana alert `TradeOpportunity` to the `owner-email` contact point |

An intent expires 5 minutes after it is created (`INTENT_TTL`), and the signal must be
younger than 5 minutes when you approve (`RISK_SIGNAL_MAX_AGE`). If a signal stays
actionable, the next scan proposes a fresh intent, so a missed email is not lost for long.

## Rules

- Symbols and strategies: the risk allowlists (`RISK_SYMBOL_ALLOWLIST`, `RISK_STRATEGY_ALLOWLIST`).
- At most one pending intent per symbol: a symbol with a pending intent is skipped.
- Skipped entirely when the market is closed, the clock cannot be read, or the kill switch is
  engaged (or its state cannot be read).
- It shares the intent rate limit (`INTENT_RATE_LIMIT_PER_HOUR`, 10 per actor) as actor
  `scanner` (type `system`); when the limit is hit the scan stops until the next one.
- Every proposal is audited like any other (`intent.created`, `created_by` = `scanner`).

## Settings (bot-engine)

| Variable | Fly | Meaning |
|---|---|---|
| `SCANNER_ENABLED` | `true` | run the scanner (default `false`) |
| `SCANNER_INTERVAL` | unset (5m) | time between scans, 1m–1h |

To stop it: set `SCANNER_ENABLED = "false"` in `bot-engine/fly.toml` and deploy, or engage the
kill switch (that also stops every proposal).

## Metrics and alerts

| Metric | Meaning |
|---|---|
| `scanner_opportunities_total` | proposals that passed risk and wait for approval |
| `scanner_last_scan_timestamp_seconds` | last scan, including ones skipped because the market is closed |

| Alert | Severity | Action |
|---|---|---|
| `TradeOpportunity` | info | Open the app's Opportunities tab and approve or reject before the countdown ends |
| `ScannerStale` | warning | No scan for 15 minutes. Check `flyctl logs -a trading-bot-engine` for `scanner.` lines |

Both are in [trading.rules.yml](../../infra/monitoring/prometheus/rules/trading.rules.yml) and must
also exist in Grafana Cloud (folder `signal-trading`, group `trading`), filtered by
`app="trading-bot-engine"`, like the other rules ([PAPER_TRACK_RECORD.md](PAPER_TRACK_RECORD.md#where-the-alerts-run-fly)).

## Logs

`scanner.scanned` (per scan: proposed, risk_rejected, signal_errors, already_pending),
`scanner.opportunity` (intent ID, symbol, strategy, side), `scanner.stopped` (rate limit or
kill switch), `scanner.signal_failed`.
