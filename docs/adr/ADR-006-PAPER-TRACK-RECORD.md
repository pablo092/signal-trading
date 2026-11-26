# ADR-006 — Paper Trading Track Record

| Field | Value |
|---|---|
| Status | **Proposed** |
| Date | 2026-11-26 |
| Deciders | Repository owner |
| Related | ADR-005 (execution safety), BL-30 (live trading), M4 |

## Context
M3 shipped order intents with mandatory human approval, a fail-closed risk engine, a single writer and a fill reconciler. On Fly, `ORDER_EXECUTION_ENABLED=false`: intents can be proposed and rejected, but nothing reaches the broker. The roadmap says live trading (BL-30) needs "a paper track record and all M3 controls", without defining either.

## Problem
Define when paper execution may be switched on, what is measured while it runs, what stops it, and what a track record must show before anyone may even write a live-trading ADR.

## Options considered
1. **Switch execution on now and watch.** Fast, but no persistence on Fly (intents and the audit trail vanish on restart) and no agreed evidence.
2. **Phased paper operation with entry gates and stop rules.** Execution stays off until persistence, alerting and a kill-switch drill exist; then a fixed observation window with written metrics.
3. **Never execute; keep proposing only.** Safe, but produces no fill, slippage or reconciliation evidence.

## Decision (proposed)
Option 2. This ADR never authorizes live trading: `PAPER_TRADING=true` and the paper-host guard (ADR-005 §5.1) stay non-negotiable.

### 6.1 Phase 0: dry run (now)
Execution off. Agents and the owner propose intents; the owner approves nothing (approval is refused) but reviews risk outcomes. Measured: intents per day, risk rejections by check (`risk_check_failures_total`).

### 6.2 Entry gate for phase 1 (all required)
1. Durable persistence: `DATABASE_URL` set (Supabase per [SUPABASE.md](../runbooks/SUPABASE.md)), migrations applied, a restart keeps intents, orders and the audit trail.
2. A human token with `intent:approve`, held only by the owner; no other principal has it.
3. A kill-switch drill: engage, see open orders canceled and new intents refused, release (documented in the track-record log).
4. Alerts from [trading.rules.yml](../../infra/monitoring/prometheus/rules/trading.rules.yml) evaluated somewhere the owner sees them.
5. The owner records the decision (date, commit) in the track-record log, then sets `ORDER_EXECUTION_ENABLED=true` on paper.

### 6.3 Phase 1: paper execution
- Window: at least **20 trading days** and at least **30 filled orders**, whichever is later.
- Limits stay at the M3 defaults ($1000 cap, 5 % equity, allowlist, regular hours, 10 intents/h). Changing any limit restarts the window.
- Weekly review using [PAPER_TRACK_RECORD.md](../runbooks/PAPER_TRACK_RECORD.md): outcomes, fill rate, slippage vs signal price, approval latency, reconciliation incidents, paper equity and max drawdown.

### 6.4 Stop rules (engage the kill switch, then investigate)
- Any order submitted twice for one intent, or any order the engine did not approve.
- An unknown submit outcome not resolved by the reconciler within 10 minutes.
- `ReconcilerPollStale` firing.
- Paper drawdown above **5 %** of the equity at phase-1 start.

A stop resets the window unless the cause is outside the engine (broker outage), recorded as such.

### 6.5 Exit
A completed window produces a written track-record summary. It is the only input that allows drafting a live-trading ADR (BL-30); it does not approve one.

## Rationale
Entry gates remove the known gaps (no persistence on Fly, no alert routing) before the first fill. A fixed window with explicit numbers turns "a paper track record" into something reviewable. Stop rules reuse controls that already exist (kill switch, reconciler metrics).

## Consequences
- Phase 1 needs a managed Postgres (Supabase free tier is enough; it pauses after a week idle, which the engine treats as a startup failure).
- The owner must approve every order by hand for the whole window; throughput stays low by design.
- The numbers (20 days, 30 fills, 5 %) are proposals for the owner to accept or change.

## Security implications
No new capability: approval stays human-only and paper-only. Enabling execution is an explicit, logged owner action. The track-record log must not contain tokens or account identifiers.

## Alternatives rejected
- Automatic approval for small orders: contradicts ADR-005 §5.3 (mandatory human approval).
- Measuring on a local machine only: restarts and laptop sleep would corrupt the window.
