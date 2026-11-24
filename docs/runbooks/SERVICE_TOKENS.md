# Runbook: service tokens (ADR-003, M1)

bot-engine and signal-service authenticate every `/api/v1/*` request (and bot-engine's `/ws`)
with a scoped static token sent as `Authorization: Bearer <token>`. Both services read the
**same token table format and rules**, so one table can serve both.
Public routes: `/health/live`, `/health/ready`, `/metrics` (both), plus `/docs`, `/redoc`,
`/openapi.json` (signal-service). **With no tokens configured, every protected request returns 401.**

## Scopes

| Scope | Routes | Who may hold it |
|---|---|---|
| `market:read` | signal-service `POST /api/v1/signals/generate`, `GET /api/v1/signals/strategies` | agent, human, service |
| `portfolio:read` | `GET /api/v1/trading-mode`, `/portfolio/equity`, `/portfolio/positions`, `GET /ws` | agent, human, service |
| `orders:place-legacy` | `POST /api/v1/orders/place` (deprecated, removed in M3) | **human only** |
| `intent:create` | M3 | agent, human |
| `intent:approve`, `admin:killswitch` | M2/M3 | **human only** |

Agent tokens holding a human-only scope make the engine refuse to start.

## Issue a token

1. Generate a random token and keep it in the caller's secret store (never in git):
   ```sh
   TOKEN=$(openssl rand -hex 32)
   ```
2. Compute its SHA-256 digest. Only the digest is given to bot-engine:
   ```sh
   printf '%s' "$TOKEN" | sha256sum | cut -d' ' -f1
   ```
3. Add an entry to the token table (JSON array):
   ```json
   [
     {"id": "mcp-gateway", "type": "agent", "sha256": "<digest>", "scopes": ["market:read", "portfolio:read"]},
     {"id": "owner",       "type": "human", "sha256": "<digest>", "scopes": ["portfolio:read"]}
   ]
   ```
4. Provide the table through one of:
   - `AUTH_TOKENS` (inline JSON). In Compose both services read it from `SERVICE_AUTH_TOKENS` in your local `.env`.
   - `AUTH_TOKENS_FILE` (path to a JSON file, e.g. a mounted secret). `AUTH_TOKENS` wins if both are set.
   - Fly.io: `fly secrets set AUTH_TOKENS='[...]' -a <app>`.
5. Restart the services. An invalid table is fatal at startup. The error names the entry `id`, never the content.

## bot-engine → signal-service

bot-engine calls signal-service to get signals, so it needs its own token:
add an entry such as `{"id": "bot-engine", "type": "service", "sha256": "<digest>", "scopes": ["market:read"]}`
to the table and give bot-engine the plaintext token in `SIGNAL_SERVICE_TOKEN`
(Compose: `BOT_ENGINE_SIGNAL_TOKEN`).

## Rotate or revoke

Add the new entry, deploy, switch the caller to the new token, then remove the old entry and deploy again.
To revoke, remove the entry and restart.

## Order execution

`POST /api/v1/orders/place` additionally requires `ORDER_EXECUTION_ENABLED=true` (default `false`).
In M1 it stays `false`; the route answers `403 execution_disabled`.

## Audit

Every protected request logs one `audit.request` line with `actor_id`, `actor_type`, `route`, `scope`,
`outcome` (`unauthenticated|forbidden|allowed`), `status`, and `request_id`. Tokens are never logged.

## Known limitation

Browsers cannot set `Authorization` on a WebSocket upgrade, so the Flutter web app's `/ws` feed returns
401 until BL-15 (authenticated upgrade and origin allowlist) lands.
