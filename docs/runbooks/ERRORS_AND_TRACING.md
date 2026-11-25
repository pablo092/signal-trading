# Errors and request tracing (BL-20)

## Error envelope

bot-engine and signal-service answer every error with the same JSON body:

```json
{"error": "insufficient_scope", "detail": "the token lacks the scope this route requires",
 "request_id": "mcp-agent-3f9c0d1e2a4b5c6d", "required_scope": "orders:place-legacy"}
```

- `error`: a stable code. Clients branch on it; codes are never renamed.
- `detail`: a fixed, safe message. Broker responses, decoder errors and stack traces are
  logged with the request ID, never returned.
- `request_id`: the same value as the `X-Request-ID` response header.
- Extra fields only where documented (`required_scope` on `insufficient_scope`).

| Code | HTTP | Service | Meaning |
| --- | --- | --- | --- |
| `unauthorized` | 401 | both | Missing or unknown bearer token |
| `insufficient_scope` | 403 | both | Token lacks `required_scope` |
| `execution_disabled` | 403 | bot-engine | `ORDER_EXECUTION_ENABLED=false` |
| `invalid_request` | 400 / 422 | both | Malformed body or parameter; `detail` names the field |
| `strategy_not_found` | 422 | signal-service | Unknown strategy; `detail` lists the available ones |
| `insufficient_data` | 422 | signal-service | Not enough bars for the strategy |
| `broker_unavailable` | 502 | bot-engine | Alpaca did not answer or failed |
| `market_data_unavailable` | 503 | signal-service | Alpaca market data failed or timed out |
| `not_found` / `method_not_allowed` | 404 / 405 | both | Unknown route or method |
| `internal_error` | 500 | both | Unexpected failure (panic or unhandled exception) |

## Following a request

The ID is the caller's `X-Request-ID` when it matches `^[A-Za-z0-9._-]{1,64}$`, otherwise a
new UUID. It is propagated on every hop and appears in each log line:

| Origin | ID format | Where it is logged |
| --- | --- | --- |
| agent-gateway | `<agent_id>-<16 hex>` per tool call | gateway audit (`request_id`) |
| Vercel proxy | `vercel-<uuid>` | browser response header |
| bot-engine | caller's ID or UUID; forwarded to signal-service | `http.request`, `audit.request` |
| signal-service | caller's ID or UUID | every structlog line of the request |

```sh
# Fly: find every log line of one request in both services
flyctl logs -a trading-bot-engine --no-tail | grep '<request_id>'
flyctl logs -a trading-bot-signal-service --no-tail | grep '<request_id>'
```
