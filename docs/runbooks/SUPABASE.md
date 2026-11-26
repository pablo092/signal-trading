# Runbook: Postgres on Supabase

bot-engine can persist orders, intents, the kill switch and the audit trail in a Supabase
Postgres. Without `DATABASE_URL` everything stays in memory and is lost on restart.

## Requirements the engine enforces

When `DATABASE_URL` points at `*.supabase.co` or `*.supabase.com`, bot-engine refuses to start unless:

| Requirement | Why |
|---|---|
| Port `5432` (direct connection or **session** pooler), not `6543` | The transaction pooler breaks the migrator's advisory lock and pgx prepared statements |
| `sslmode=require` (or `verify-ca` / `verify-full`) | The connection crosses the internet from Fly |
| `DATABASE_SCHEMA` set to a dedicated schema (e.g. `tradingbot`), not `public` | Supabase's Data API exposes `public`; engine tables have no row-level security |

The engine creates the schema if it does not exist and applies its migrations there on every
start (idempotent, under an advisory lock). Errors never print the URL.

## Setup

1. Create a Supabase project. In **Settings → API → Data API**, either disable the Data API or make
   sure `tradingbot` is **not** in the exposed schemas.
2. Copy the **Session pooler** connection string (IPv4, port 5432) from **Connect**. The direct
   connection (`db.<ref>.supabase.co`) is IPv6-only on the free plan; Fly can reach it too.
3. Append `?sslmode=require`. Keep the string out of the terminal history and out of the repo.
4. Set the secrets yourself (they restart the engine):

   ```sh
   flyctl secrets set DATABASE_SCHEMA=tradingbot -a trading-bot-engine
   flyctl secrets set DATABASE_URL='postgres://postgres.<ref>:<password>@aws-0-<region>.pooler.supabase.com:5432/postgres?sslmode=require' -a trading-bot-engine
   ```

5. Check the logs for `postgres.ready` with `"schema":"tradingbot"` and the applied migrations,
   then restart once more and confirm `GET /api/v1/order-intents` still lists earlier intents.

Locally the same variables work with Compose (`BOT_ENGINE_DATABASE_URL`, `DATABASE_SCHEMA`).

## Operations

- **Free plan pause:** a project idle for a week is paused. The engine then fails to start
  (it fails closed on migrations); resume the project in the dashboard and restart.
- **Backups:** daily on paid plans only. For the track record (ADR-006), export weekly:
  `pg_dump --schema=tradingbot "$DATABASE_URL" > tradingbot-YYYYMMDD.sql` (stored privately).
- **Audit trail:** `audit_events` refuses UPDATE, DELETE and TRUNCATE through triggers. Do not
  disable them; the Supabase dashboard's table editor will fail on that table by design.
- **Rotating the password:** reset it in Supabase, then `flyctl secrets set DATABASE_URL=...` again.
- **Leaving Supabase:** any Postgres 14+ works; keep `DATABASE_SCHEMA` or drop it (then the
  server's default `search_path` is used).
