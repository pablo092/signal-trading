# Runbook: kill switch (ADR-005 §5.7, BL-19)

The kill switch stops bot-engine from sending orders. While it is engaged, every
`POST /api/v1/order-intents` answers `409 kill_switch_engaged` before the signal is even
fetched, and approving an existing intent rejects it with `rejection_code: kill_switch`
(the submission-stage risk check fails). Engaging it also cancels all open orders at the broker.

It **fails closed**: if its state cannot be read (database down), the engine treats it as
engaged.

## Who can use it

| Action | Route | Scope |
| --- | --- | --- |
| Read state | `GET /api/v1/admin/kill-switch` | `portfolio:read` |
| Engage / release | `POST /api/v1/admin/kill-switch` | `admin:killswitch` (**human tokens only**, ADR-003) |

On Fly, `owner` and `operator` hold `admin:killswitch` (since 2026-11-26, ADR-006 drill). To grant
it elsewhere, add the scope to a `human` entry of `AUTH_TOKENS` (see [SERVICE_TOKENS.md](SERVICE_TOKENS.md))
and restart. A token table that grants it to an `agent` or `service` entry fails to load, and
such tokens are refused at request time as well. Give it only to `human` entries.

## Engage

```sh
curl -sS -X POST "$ENGINE/api/v1/admin/kill-switch" \
  -H "Authorization: Bearer $OPERATOR_TOKEN" -H 'Content-Type: application/json' \
  -d '{"engaged": true, "reason": "broker incident 2026-11-25"}'
```

`reason` is required (1–200 characters) and is stored in the audit trail. The order of
effects is:

1. The in-process latch flips, so new orders stop **immediately**, even if the next steps fail.
2. The state is persisted (Postgres when `DATABASE_URL` is set, otherwise in memory).
3. `CancelAllOrders` is called at the broker (15 s timeout).
4. A `kill_switch.engaged` audit event is recorded, `ALERT kill_switch.engaged` is logged at
   error level, and a `kill_switch.changed` event goes to WebSocket clients.

The response reports `persisted` and `cancel_all`. **Check both:** `cancel_all` other than
`"ok"` means open orders may remain at the broker; cancel them in the Alpaca dashboard.

## Release

```sh
curl -sS -X POST "$ENGINE/api/v1/admin/kill-switch" \
  -H "Authorization: Bearer $OPERATOR_TOKEN" -H 'Content-Type: application/json' \
  -d '{"engaged": false, "reason": "incident closed"}'
```

Release only succeeds if the new state is written; otherwise it answers
`503 kill_switch_unavailable` and the switch stays engaged.

## Environment override (durable, no API needed)

`KILL_SWITCH=true` forces the switch on at startup. While it is set, the API cannot release
it (`409 kill_switch_forced`). Use it when the API or the token is unavailable:

```sh
flyctl secrets set KILL_SWITCH=true -a trading-bot-engine    # restarts the machines
flyctl secrets unset KILL_SWITCH -a trading-bot-engine       # then release through the API
```

## Without a database

Without `DATABASE_URL` (local runs; on Fly it is set since 2026-11-26) an engaged switch
**does not survive a restart or redeploy**. If it must stay on across restarts, use `KILL_SWITCH=true`.
Audit events are then kept only in the log (`audit.event` lines with `payload_sha256`).

## Monitoring

`bot_engine_kill_switch_engaged` is 1 while orders are blocked. Alert on it, and on the
`ALERT kill_switch.engaged` log line.
