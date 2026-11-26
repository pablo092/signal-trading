# Runbook: paper trading track record (ADR-006)

How to run and review the paper track record. Paper only: nothing here enables live trading.

## Phases

| Phase | `ORDER_EXECUTION_ENABLED` | What happens |
|---|---|---|
| 0, dry run (current on Fly) | `false` | Intents are proposed and risk-checked; approval answers `403 execution_disabled` |
| 1, paper execution | `true` (paper host only) | The owner approves intents; orders go to the Alpaca paper account |

## Entry checklist (phase 0 → 1)

Tick every item in the track-record log (date, who, commit):

1. Persistence: `DATABASE_URL` and `DATABASE_SCHEMA` set ([SUPABASE.md](SUPABASE.md)). Restart the engine and check
   that an intent created before the restart is still listed (`GET /api/v1/order-intents`).
2. Approver token: one `human` entry with `intent:approve` in `AUTH_TOKENS` ([SERVICE_TOKENS.md](SERVICE_TOKENS.md)).
3. Kill-switch drill ([KILL_SWITCH.md](KILL_SWITCH.md)): engage, check `cancel_all: ok` and that a new intent gets
   `409 kill_switch_engaged`, release.
4. Alerts: the rules in `infra/monitoring/prometheus/rules/trading.rules.yml` are evaluated where you see them.
5. Enable: `flyctl secrets set ORDER_EXECUTION_ENABLED=true -a trading-bot-engine` (or change `[env]` in
   `bot-engine/fly.toml`) and confirm `GET /api/v1/trading-mode` shows `"order_execution_enabled": true` and `"mode": "paper"`.

To go back to phase 0 at any time: set it to `false` again. Pending intents stay; approval is refused.

## Metrics

All on bot-engine `GET /metrics`. Labels are bounded: no symbols, actors or IDs.

| Metric | Meaning |
|---|---|
| `order_intent_transitions_total{status,rejection_code}` | intents entering each status |
| `risk_check_failures_total{check,stage}` | failed checks on risk-rejected intents (`stage` = creation or submission) |
| `orders_filled_total{side}` | fills reported by the broker |
| `reconciler_orders_reconciled_total{source}` | local orders corrected from the broker: `stream`, `poll`, `not_placed` |
| `reconciler_trade_stream_up` | 1 while the trade-update stream runs |
| `reconciler_trade_stream_disconnects_total` | stream reconnects |
| `reconciler_last_poll_timestamp_seconds` | last completed reconciliation poll |
| `bot_engine_kill_switch_engaged` | 1 while trading is halted |

## Alerts

| Alert | Severity | Action |
|---|---|---|
| `ReconcilerPollStale` | critical | Engine stuck or crash-looping. Check `flyctl logs`; **stop rule** (ADR-006 §6.4): engage the kill switch until polls resume |
| `TradeStreamDown` | warning | Polling still reconciles every 30 s. Check Alpaca status; nothing to do unless it lasts hours |
| `OrderNeverReachedBroker` | warning | An approved order was closed as never placed (unknown submit outcome, not at the broker). Review the intent and the audit trail; re-propose if still wanted |
| `BrokerRejectedIntent` | warning | Alpaca refused an approved order (e.g. buying power). Check the account |
| `RiskRejectionsHigh` | info | Many proposals fail risk. Look at `risk_check_failures_total` by `check` |

## Weekly review

Queries for the Postgres database (run in the engine schema, e.g. `SET search_path = tradingbot;`).
They only read.

Outcomes:

```sql
SELECT status, doc->>'rejection_code' AS rejection_code, count(*)
FROM order_intents
WHERE created_at >= now() - interval '7 days'
GROUP BY 1, 2 ORDER BY 3 DESC;
```

Slippage of filled orders vs the signal price (positive = paid more on a buy / got less on a sell):

```sql
SELECT o.symbol, o.side, (i.doc->'signal'->>'price')::numeric AS signal_price, o.filled_price,
       round(CASE o.side WHEN 'buy' THEN o.filled_price - (i.doc->'signal'->>'price')::numeric
                         ELSE (i.doc->'signal'->>'price')::numeric - o.filled_price END
             / (i.doc->'signal'->>'price')::numeric * 10000, 1) AS slippage_bps
FROM orders o JOIN order_intents i ON i.id = o.id
WHERE o.status = 'filled' AND o.filled_at >= now() - interval '7 days'
ORDER BY o.filled_at;
```

Approval latency (signal generated → human decision):

```sql
SELECT percentile_cont(ARRAY[0.5, 0.9]) WITHIN GROUP (
         ORDER BY extract(epoch FROM ((doc->>'decided_at')::timestamptz - created_at)))
         AS p50_p90_seconds
FROM order_intents
WHERE doc->>'decided_at' IS NOT NULL AND created_at >= now() - interval '7 days';
```

Reconciliation incidents:

```sql
SELECT action, result, count(*) FROM audit_events
WHERE action IN ('order.reconciled', 'order.outcome_unknown', 'order.rejected')
  AND occurred_at >= now() - interval '7 days'
GROUP BY 1, 2;
```

Paper equity and drawdown: read the account in the Alpaca paper dashboard (or
`GET /api/v1/portfolio/equity`) once a day and record it in the log.

## Track-record log

Keep it outside the repo or in a private note: date, commit, phase, the entry-checklist items,
weekly numbers (intents, fills, fill rate, median slippage, p90 approval latency, incidents,
equity, max drawdown), every stop and its cause. Never paste tokens or account numbers.
