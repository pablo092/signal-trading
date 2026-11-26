# Fly.io deployment and web demo

| Component | Where | Exposure |
|---|---|---|
| signal-service | `trading-bot-signal-service` (Fly, `iad`) | `https://trading-bot-signal-service.fly.dev`, token required; auto-stops when idle |
| bot-engine | `trading-bot-engine` (Fly, `iad`) | `https://trading-bot-engine.fly.dev`, token required; always on |
| Flutter web | Vercel (repo-root `vercel.json`) | public UI; signals via the `api/` proxy |

bot-engine calls signal-service privately over Flycast (`http://trading-bot-signal-service.flycast`).
Order execution is disabled (`ORDER_EXECUTION_ENABLED=false`) and the paper guard is enforced.

## Secrets

Set with `flyctl secrets import -a <app>` from stdin; never commit or print values.

| Secret | signal-service | bot-engine |
|---|---|---|
| `ALPACA_API_KEY`, `ALPACA_SECRET_KEY` (paper) | yes | yes |
| `AUTH_TOKENS` (shared table, see [SERVICE_TOKENS.md](SERVICE_TOKENS.md)) | yes | yes |
| `SIGNAL_SERVICE_TOKEN` | | yes |

Token table entries in production: `operator` (human; `market:read`, `portfolio:read`),
`bot-engine` (service; `market:read`), `vercel-demo` (service; `market:read`).
Plaintext tokens live only in the operator's git-ignored `secrets/fly-tokens.env`.

## Web demo

The browser cannot hold a service token (ADR-003), so the Vercel function
[api/v1/signals/generate.js](../../api/v1/signals/generate.js) holds the `vercel-demo`
token server-side (Vercel env var `SIGNAL_SERVICE_TOKEN`) and forwards validated
`POST /api/v1/signals/generate` calls. The app is built with
`SIGNAL_SERVICE_URL=same-origin` and the live WebSocket feed disabled until BL-15.

To revoke the demo: remove the `vercel-demo` entry from `AUTH_TOKENS` on both apps.

## M2 runtime settings

| Setting | Fly value | Effect |
|---|---|---|
| `DATABASE_URL` | unset | in-memory orders and kill switch; audit events go to the log only. An engaged kill switch does not survive a restart: see [KILL_SWITCH.md](KILL_SWITCH.md) |
| `KILL_SWITCH` | unset | set `true` (as a secret, restarts the machines) to force-stop orders durably |
| `WS_ALLOWED_ORIGINS` | unset | no browser origin may open `/ws` (the web feed is disabled anyway) |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | unset | no traces exported (no collector on Fly) |

## M3 runtime settings

| Setting | Fly value | Effect |
|---|---|---|
| `ORDER_EXECUTION_ENABLED` | `false` | intents can be created and rejected, but **approval is refused** (`execution_disabled`); nothing reaches the broker |
| `INTENT_TTL`, `RISK_SIGNAL_MAX_AGE` | unset (5m) | pending intents expire after 5 minutes; signals older than 5 minutes fail `signal_fresh` |
| `RISK_MAX_ORDER_NOTIONAL` | unset ($1000) | absolute cap per order, on top of 5 % of equity |
| `RISK_SYMBOL_ALLOWLIST` | unset (`SPY,QQQ,AAPL,MSFT`) | set-but-empty refuses every symbol |
| `INTENT_RATE_LIMIT_PER_HOUR` | unset (10) | per actor, in memory |

Intents live in memory without `DATABASE_URL` and are lost on restart. Enabling execution is a
separate, explicit decision: see [ORDER_INTENTS.md](ORDER_INTENTS.md).

Adding Postgres later: follow [SUPABASE.md](SUPABASE.md) (or any Postgres 14+), set `DATABASE_SCHEMA`
and then `flyctl secrets set DATABASE_URL=... -a trading-bot-engine`.
The engine applies its migrations on start and refuses to start if they fail.

## Deploy

```sh
make deploy-fly        # both services
flyctl status -a trading-bot-engine
```
