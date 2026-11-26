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
| `portfolio:read` | `GET /api/v1/trading-mode`, `/portfolio/equity`, `/portfolio/positions`, `GET /ws`, `GET /api/v1/order-intents[/{id}]`, `GET /api/v1/admin/kill-switch` | agent, human, service |
| `orders:place-legacy` | none: `POST /api/v1/orders/place` was removed in M4. Still accepted in token tables so old tables load; drop it when you next edit the table | **human only** |
| `intent:create` | `POST /api/v1/order-intents`, `POST /api/v1/order-intents/{id}/cancel` (own intents only) | agent, human |
| `intent:approve` | `POST /api/v1/order-intents/{id}/approve` and `/reject` | **human only** |
| `admin:killswitch` | `POST /api/v1/admin/kill-switch` | **human only** |

Only `human` entries may hold a human-only scope: an `agent` or `service` entry with one makes the
engine refuse to start, and such a token is also refused at request time. The MCP gateway token
needs `market:read`, `portfolio:read` and `intent:create`; it can propose intents but never decide them.

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

## MCP agent gateway

The [agent-gateway](../../agent-gateway/README.md) uses one `agent` token with only
`market:read` and `portfolio:read`. For example:
`{"id": "mcp-gateway", "type": "agent", "sha256": "<digest>", "scopes": ["market:read", "portfolio:read"]}`.
Give the plaintext token to the MCP client config as `GATEWAY_TOKEN`. Issue one token per
agent if you want separate audit identities and separate revocation.

## bot-engine → signal-service

bot-engine calls signal-service to get signals, so it needs its own token:
add an entry such as `{"id": "bot-engine", "type": "service", "sha256": "<digest>", "scopes": ["market:read"]}`
to the table and give bot-engine the plaintext token in `SIGNAL_SERVICE_TOKEN`
(Compose: `BOT_ENGINE_SIGNAL_TOKEN`).

## Rotate or revoke

Add the new entry, deploy, switch the caller to the new token, then remove the old entry and deploy again.
To revoke, remove the entry and restart.

## Order execution

Approving an intent (`POST /api/v1/order-intents/{id}/approve`) additionally requires
`ORDER_EXECUTION_ENABLED=true` (default `false`); otherwise it answers `403 execution_disabled`.
Creating, rejecting and canceling intents work either way. See [ORDER_INTENTS.md](ORDER_INTENTS.md).

## Audit

Every protected request logs one `audit.request` line with `actor_id`, `actor_type`, `route`, `scope`,
`outcome` (`unauthenticated|forbidden|allowed`), `status`, and `request_id`. Tokens are never logged.

Order and kill-switch decisions are also recorded as audit events (BL-14) with the same `actor_id`:
in the append-only `audit_events` table when `DATABASE_URL` is set, otherwise as `audit.event` log lines.

## WebSocket (`/ws`, BL-15)

`/ws` needs `portfolio:read`. Native clients send `Authorization: Bearer <token>`. Browsers cannot set
that header on an upgrade, so the token may instead be sent as the subprotocol pair
`Sec-WebSocket-Protocol: bearer, <token>`; the server echoes only `bearer`. A token in the query string
is never accepted (it would end up in proxy logs). Browser origins must be listed in
`WS_ALLOWED_ORIGINS` (comma-separated; empty = none).

The public web demo still has the feed disabled: a browser must not hold a long-lived token (ADR-003),
so enabling it needs a short-lived ticket issued server-side (not built yet).
