# ADR-005 — Trading Execution Safety

| Field | Value |
|---|---|
| Status | **Proposed** |
| Date | 2026-11-24 |
| Deciders | Repository owner |
| Related | TD-01…TD-06, SEC-01…SEC-05, SEC-11, ADR-003, ADR-004 |

## Context
Today one unauthenticated HTTP call places a market order sized at 5 % of equity, against whichever Alpaca host the env var names. There is no deduplication, an incomplete risk check that fails open on PnL errors, no persistence, and no way to stop trading (SECURITY_AUDIT §5). Agents will eventually be able to *propose* trades.

## Problem
Define the controls that every order must pass: approval workflow, idempotency, risk validation, mode guard, and emergency shutdown.

## Options considered
1. **Direct execution with stronger checks.** Keep `POST /orders/place` and add auth, idempotency, and risk.
2. **Order-intent pipeline.** Callers create *intents*; intents pass risk checks, optional or mandatory human approval, then a single executor submits them.
3. **Human-only execution.** Agents and automation never create orders.

## Decision (proposed)
Option 2, with the following non-negotiable controls.

### 5.1 Mode guard (applies immediately, M1)
- Startup fails unless `PAPER_TRADING=true` **and** the broker host is the paper host. Any mismatch is fatal.
- Live trading requires a future ADR, `TRADING_MODE=live`, and `LIVE_TRADING_ACKNOWLEDGED=<ISO date>`. Without both, startup fails.
- `GET /api/v1/trading-mode` reports the *verified* mode. The hardcoded `paper_mode: true` is removed.

### 5.2 Execution disabled by default (M1)
- `ORDER_EXECUTION_ENABLED=false` by default. When false, all order routes return `403 execution_disabled`.

### 5.3 Order intents and approval (M3)
- `POST /api/v1/order-intents` (scope `intent:create`) persists an intent: `pending_risk → pending_approval → approved → submitted → filled|rejected|canceled|expired`.
- **Approval policy:** initially *every* intent requires human approval (`intent:approve`). Auto-approval may later be enabled per strategy, below a notional threshold, by config plus ADR amendment.
- Intents expire after a configurable TTL (default 5 minutes) if not approved.

### 5.4 Idempotency (M3)
- `Idempotency-Key` header is required on intent creation. `(actor, key)` is unique, and a replay returns the original response.
- At most one active intent per `signal_id`.
- `client_order_id = intent_id` on broker submission. On timeout or crash, recover with `GetOrderByClientID` (ADR-004) before any retry. **Never resubmit blindly.**

### 5.5 Risk validation (fail-closed, M3)
All of the following are evaluated at intent creation **and again** immediately before submission. If any input is unavailable, the intent is rejected.

| Check | Source |
|---|---|
| Kill switch not engaged | DB flag |
| Verified paper mode | 5.1 |
| Market open (or extended hours explicitly allowed) | broker clock |
| Signal fresh (age < N seconds) and confidence ≥ threshold | signal metadata |
| Daily realized + unrealized loss < `MaxDailyLossPct` | broker account / trades table |
| Open positions < `MaxOpenPositions` | broker positions |
| Position notional ≤ `MaxPositionSizePct` × equity **and** ≤ absolute cap | config |
| SELL quantity ≤ held quantity unless shorting is enabled | positions + capabilities |
| Symbol and strategy allowlists | config |
| Per-actor order rate limit | counter |
| `RiskRules.Validate()` passes at startup | config |

Stop-loss and take-profit are attached as broker-side bracket orders when supported (ADR-004 capabilities).

### 5.6 Single writer
- Exactly one executor instance submits orders. K8s `replicas: 1`, `strategy: Recreate`, no HPA. If scaling is ever needed, use a DB advisory lock for leader election.

### 5.7 Emergency shutdown (kill switch, M2)
- A persisted flag, settable via `POST /api/v1/admin/kill-switch` (scope `admin:killswitch`, humans only) and via the env override `KILL_SWITCH=true` at startup.
- Engaging it rejects new intents, cancels open broker orders (`CancelAllOrders`), and emits an alert and an audit event.
- Releasing it requires a separate explicit call and is audited.

### 5.8 Audit (M2)
- Append-only `audit_events` for every intent transition, approval, risk decision (with the inputs used), broker call, and kill-switch change.

## Rationale
- Intents separate *deciding* from *doing*. That is the boundary where agents, humans, and risk logic meet.
- Fail-closed checks invert today's fail-open behavior (`place_order.go:113-115`).
- Double risk evaluation covers state changes between proposal and approval.
- Mandatory approval at first is cheap to relax later, and impossible to retrofit after an incident.

## Consequences
- `POST /api/v1/orders/place` is deprecated. It is disabled in M1 and removed once intents exist. **This is a public API change and requires this ADR's acceptance.**
- The app's "Place Order" button (currently a no-op) becomes "Propose trade".
- Requires Postgres persistence (M2) and BrokerPort v2 (ADR-004).

## Security implications
- Closes SEC-01 to SEC-05 and SEC-11 when fully implemented.
- The approval UI or API itself becomes a high-value target. It must be human-scoped (ADR-003) and audited.
- The kill switch must work even if the signal-service or the app is down. It depends only on the DB and the broker.

## Alternatives rejected
- **Option 1 (direct execution, hardened):** no human checkpoint and no natural place for agent proposals. One bug or injection means an order.
- **Option 3 (human-only):** safe, but contradicts the product goal. Option 2 with mandatory approval provides the same safety initially.

## Open questions
- Approval channel: the app only, or also a messaging integration (e.g. Slack)?
- Intent TTL default and signal freshness window per timeframe.
- Absolute notional cap value for paper trading.
