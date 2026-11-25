# Runbook: order intents (ADR-005 §5.3–5.5, M3)

An **order intent** is a proposed order. Agents and humans propose; only a human approves;
only the engine's single executor submits. Every intent needs a human approval (no
auto-approval). Everything stays on Alpaca **paper**.

```text
POST /order-intents ──► pending_risk ──risk fails──► rejected (risk_failed)
                            │ risk passes
                            ▼
                     pending_approval ──TTL──► expired
                            │  │  └── reject (human) / cancel (creator) ──► rejected / canceled
                            │ approve (human, ORDER_EXECUTION_ENABLED=true)
                            ▼
                         approved ──risk re-check fails / broker refuses──► rejected
                            │ broker accepted   (outcome unknown: stays approved until reconciled)
                            ▼
                        submitted ──fills / cancels (reconciler, BL-23)──► filled / canceled / expired
```

## API

All routes are under `/api/v1`. Errors use the standard envelope
(`{"error","detail","request_id"}`, see [ERRORS_AND_TRACING.md](ERRORS_AND_TRACING.md)).
Bodies follow [`contracts/schemas/order-intent.schema.json`](../../contracts/schemas/order-intent.schema.json).

| Method and path | Scope | Notes |
|---|---|---|
| `POST /order-intents` | `intent:create` | Header `Idempotency-Key` (required, 8–128 chars `[A-Za-z0-9._:-]`). Body `{"symbol":"AAPL","strategy":"rsi"}`. The engine fetches the signal itself and sizes the order; callers never send a side or quantity. |
| `GET /order-intents?status=<s>&limit=<n>` | `portfolio:read` | Newest first; `limit` 1–200 (default 50). Body: `order-intent-list.schema.json`. |
| `GET /order-intents/{id}` | `portfolio:read` | |
| `POST /order-intents/{id}/approve` | `intent:approve` (**human only**) | Body `{"reason": "optional, ≤200 chars"}`. Re-runs every risk check, then submits. Needs `ORDER_EXECUTION_ENABLED=true`. |
| `POST /order-intents/{id}/reject` | `intent:approve` (**human only**) | Body `{"reason": "required, 1–200 chars"}`. |
| `POST /order-intents/{id}/cancel` | `intent:create` | Only the actor that created it, only while `pending_approval`. |

### Responses

| Situation | Status | `error` code |
|---|---|---|
| Intent created (passed risk → `pending_approval`, or failed risk → `rejected`) | 201 | — (inspect `status` and `risk.checks`) |
| Same `Idempotency-Key` and same body again | the original status | — the original body; header `Idempotent-Replayed: true` |
| Same key, different body | 422 | `idempotency_key_reused` |
| Missing or malformed key | 400 | `idempotency_key_required` |
| Kill switch engaged | 409 | `kill_switch_engaged` |
| An active intent already exists for this signal | 409 | `intent_exists` (`detail` names its id) |
| More than `INTENT_RATE_LIMIT_PER_HOUR` creations by this actor | 429 | `rate_limited` |
| signal-service unavailable | 502 | `signal_unavailable` |
| Approve / reject / cancel on an intent that is not `pending_approval` | 409 | `intent_not_pending` |
| Approve after `expires_at` (the intent becomes `expired`) | 409 | `intent_expired` |
| Cancel by another actor | 403 | `not_intent_owner` |
| Approve with `ORDER_EXECUTION_ENABLED=false` | 403 | `execution_disabled` |
| Unknown id | 404 | `not_found` |
| Approve: risk re-check failed or broker refused | 200 | — intent `rejected` with `rejection_code` |
| Approve: broker outcome unknown | 202 | — intent stays `approved`; the reconciler resolves it. **Do not retry.** |

Approve answers 200 with the intent (`submitted`, or `rejected` with a code) when the outcome
is known.

### WebSocket

Every transition is sent on `/ws` as `intent.updated` (data = the intent). Broker fills are
sent as `order.filled` (data = the order).

## Risk checks (ADR-005 §5.5)

Evaluated at creation and again immediately before submission. **Any unavailable input fails
the check** (fail closed). Each result is stored in `risk.checks` and in the audit log.

| Check (`name`) | Rule | Config (default) |
|---|---|---|
| `kill_switch` | not engaged | — |
| `paper_mode` | broker mode verified as paper | — |
| `market_open` | broker clock open | `RISK_ALLOW_EXTENDED_HOURS` (`false`) |
| `signal_actionable` | BUY or SELL | — |
| `signal_fresh` | signal age ≤ max age | `RISK_SIGNAL_MAX_AGE` (`5m`) |
| `confidence` | ≥ `MinConfidence` | 0.60 |
| `symbol_allowlist` | symbol listed (empty list rejects everything) | `RISK_SYMBOL_ALLOWLIST` (`SPY,QQQ,AAPL,MSFT`) |
| `strategy_allowlist` | strategy listed (empty list rejects everything) | `RISK_STRATEGY_ALLOWLIST` (`rsi,ma_cross`) |
| `account_active` | account not blocked, equity > 0 | — |
| `daily_loss` | loss since previous close < `MaxDailyLossPct` | 2 % |
| `open_positions` | a BUY in a new symbol needs open positions < `MaxOpenPositions` | 5 |
| `notional_cap` | notional ≤ `MaxPositionSizePct` × equity **and** ≤ absolute cap | 5 %, `RISK_MAX_ORDER_NOTIONAL` (`1000`) |
| `buying_power` | BUY notional ≤ buying power | — |
| `sell_covered` | SELL needs a held long position; qty = held qty (no shorting) | — |
| `quantity` | qty ≥ 0.001 | — |

Intents expire `INTENT_TTL` (`5m`) after creation if not approved. The rate limit
`INTENT_RATE_LIMIT_PER_HOUR` (`10`) is per actor.

## Executor and reconciliation

- One process submits orders (ADR-005 §5.6: `replicas: 1`, `Recreate`). Approval moves the
  intent with a compare-and-set on its status, so a double click cannot submit twice.
- `client_order_id` = intent id = order id. On a timeout the executor looks the order up with
  `GetOrderByClientID` before deciding; it never resubmits blindly.
- The reconciler follows Alpaca trade updates (SSE stream) and also polls open orders every
  30 s. It updates the order and the intent (`filled`, `canceled`, `expired`, `rejected`) and
  resolves intents left `approved` after an unknown outcome.

## Operations

```sh
# list what is waiting for approval
curl -sS "$ENGINE/api/v1/order-intents?status=pending_approval" -H "Authorization: Bearer $TOKEN"
# approve (human token with intent:approve)
curl -sS -X POST "$ENGINE/api/v1/order-intents/$ID/approve" -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"reason":"checked chart"}'
```

`POST /api/v1/orders/place` is **deprecated** (responses carry `Deprecation: true` and a
`Link` to `/api/v1/order-intents`). It bypasses approval, so it stays behind the human-only
`orders:place-legacy` scope and `ORDER_EXECUTION_ENABLED`; it will be removed.
