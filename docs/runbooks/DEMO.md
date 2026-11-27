# Runbook: platform demo

A 15-minute walkthrough of what runs today: market signals, an order intent proposed and
decided by a human, the risk engine, the kill switch, the audit trail and monitoring.
Everything is on Alpaca **paper**; nothing here can place a live order.

## What you show, and where

| Part | Where | Needs |
|---|---|---|
| Signals (public, read-only) | Web demo on Vercel (`https://agent-gateway-eta.vercel.app`) | nothing |
| Propose, approve or reject intents | Flutter app run locally against the Fly engine | the `owner` token |
| Monitoring and alerts | Grafana Cloud dashboard **signal-trading — paper ops** | Grafana login |
| Audit trail | Supabase SQL editor, schema `tradingbot` | Supabase login |

The public site never holds an engine token (ADR-003). Intents are shown from a local build
that carries the owner token only on your machine.

## Before the demo

1. **Market hours.** The risk engine refuses intents outside regular US hours
   (09:30–16:00 New York: 11:30–18:00 in Buenos Aires from November to March, 10:30–17:00 the rest of the year). Outside them, a proposal
   is still created and shown as `rejected` with the failed check, which is also worth showing.
2. **Execution mode.** Fly runs ADR-006 phase 1: an approved intent becomes a **paper** order and
   counts toward the track record ([PAPER_TRACK_RECORD.md](PAPER_TRACK_RECORD.md)). Approve only what
   you would really trade on paper; for a show-only demo, reject instead.
3. **Flutter SDK 3.41.9** (same as the Vercel build). Check `flutter --version`.
4. **Health:** `curl https://trading-bot-engine.fly.dev/health/ready` answers `"status":"ready"`.
   signal-service is always on, so the first signal does not wait for a cold start.

## Start the local app

The engine and the Vercel signal proxy accept browser calls from `http://localhost:5000` only
(`CORS_ALLOWED_ORIGINS` / `WS_ALLOWED_ORIGINS` in `bot-engine/fly.toml`). Keep that port.

In Git Bash, paste the owner token from your password manager when `read` waits (it is not echoed
and stays out of the shell history):

```sh
cd app
read -rs OWNER_TOKEN
flutter run -d chrome --web-port 5000 \
  --dart-define=SIGNAL_SERVICE_URL=https://agent-gateway-eta.vercel.app \
  --dart-define=BOT_ENGINE_URL=https://trading-bot-engine.fly.dev \
  --dart-define=BOT_ENGINE_WS_URL=wss://trading-bot-engine.fly.dev/ws \
  --dart-define=BOT_ENGINE_TOKEN="$OWNER_TOKEN" \
  --dart-define=BOT_ENGINE_WS_TOKEN="$OWNER_TOKEN"
unset OWNER_TOKEN   # after the app has started
```

Never build this for the web host: a `--dart-define` value is readable in the bundle.

## Script

1. **Signals.** On the web demo, generate a signal for `AAPL` with `rsi`: direction, strength,
   confidence and the reason (e.g. "RSI=58.4, neutral zone").
2. **Portfolio.** In the local app, the Portfolio tab reads paper equity and positions from the engine.
3. **Propose.** Orders tab → *Propose trade* `AAPL`, strategy `rsi`. The engine fetches the signal
   itself and sizes the order; the app never sends a side or quantity. Show the status and the risk
   checks (cap $1000, 5 % of equity, allowlist, market hours, rate limit).
4. **Decide.** Approvals tab lists `pending_approval` intents. *Reject* with a reason, or *Approve*:
   the order goes to the paper account and the live feed shows it filling (with execution off,
   approval answers `execution_disabled`).
5. **Kill switch.** Engage it, show a new proposal refused with `409 kill_switch_engaged`, release
   ([KILL_SWITCH.md](KILL_SWITCH.md), owner token).
6. **Audit trail.** In Supabase:
   `select occurred_at, actor_id, action, result from tradingbot.audit_events order by occurred_at desc limit 20;`
   Every decision is there, append-only (UPDATE and DELETE are refused by triggers).
7. **Monitoring.** Grafana dashboard: services up, kill switch, trade stream, last reconciliation,
   intents by status, rejections by reason. Alert rules in folder `signal-trading` email the owner.
8. **Agents (optional).** An MCP client using the agent-gateway can list strategies, generate signals
   and propose intents, but has no tool to approve them ([agent-gateway](../../agent-gateway/README.md)).

## After the demo

Close the app and `unset OWNER_TOKEN` if the shell is still open. Pending intents expire on their own.
