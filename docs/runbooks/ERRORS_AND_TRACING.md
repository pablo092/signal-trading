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
| `broker_unavailable` | 502 | bot-engine | Alpaca did not answer or failed; on the order route the order was **not** placed |
| `signal_unavailable` | 502 | bot-engine | signal-service did not return a signal for the order route |
| `order_rejected` | 422 | bot-engine | The broker refused the order (e.g. insufficient buying power); it is stored as `rejected` |
| `order_outcome_unknown` | 502 | bot-engine | The broker did not confirm the order and could not be queried; it may exist. **Do not retry**: the order stays `pending` until the reconciler resolves it (BL-23). On intent approval this is a `202` with the intent left `approved`, not an error |
| `market_data_unavailable` | 503 | signal-service | Alpaca market data failed or timed out |
| `kill_switch_engaged` | 409 | bot-engine | The kill switch is on (or its state could not be read): no order was sent. See [KILL_SWITCH.md](KILL_SWITCH.md) |
| `kill_switch_forced` | 409 | bot-engine | `KILL_SWITCH=true` is set on the engine; the API cannot release it |
| `kill_switch_unavailable` | 503 | bot-engine | The kill-switch state could not be read or written; trading stays refused |
| `audit_unavailable` | 503 | bot-engine | The audit record could not be written before submitting, so the order was **not** sent (fails closed) and is stored as `rejected` |
| `idempotency_key_required` | 400 | bot-engine | `POST /order-intents` without a valid `Idempotency-Key` header (8-128 chars `[A-Za-z0-9._:-]`) |
| `idempotency_key_reused` | 422 | bot-engine | The key was already used by this actor for a different request (symbol/strategy) |
| `intent_exists` | 409 | bot-engine | An active intent already exists for this signal |
| `intent_not_pending` | 409 | bot-engine | The intent is no longer `pending_approval` (already decided, canceled or submitted) |
| `intent_expired` | 409 | bot-engine | The intent passed its TTL before the decision; it is now `expired` |
| `not_intent_owner` | 403 | bot-engine | Only the actor that created an intent may cancel it |
| `rate_limited` | 429 | bot-engine | The actor exceeded `INTENT_RATE_LIMIT_PER_HOUR` |
| `not_found` / `method_not_allowed` | 404 / 405 | both | Unknown route, method, or order intent |
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

## Distributed traces (BL-17)

Both services export OpenTelemetry spans over OTLP/HTTP **only when**
`OTEL_EXPORTER_OTLP_ENDPOINT` (or `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT`) is set; otherwise
no exporter is created. bot-engine sends W3C `traceparent` to signal-service, so one order
request is one trace across both. Server spans carry `request_id`, so a log line leads to
its trace and back.

```sh
# Local: Jaeger accepts OTLP on 4318 (profile full)
OTEL_EXPORTER_OTLP_ENDPOINT=http://jaeger:4318 docker compose --profile full up -d
# then open http://localhost:16686 and search service bot-engine
```

Fly has no collector, so tracing is off there. Never log the endpoint or
`OTEL_EXPORTER_OTLP_HEADERS`: hosted collectors put API keys in them.

## Metrics (BL-17)

| Metric | Service | Notes |
| --- | --- | --- |
| `http_requests_total{method,path,status}` | bot-engine | `status` is numeric (`"503"`); `path` is the route template, `unknown` when unmatched |
| `http_requests_total{method,endpoint,status}` | signal-service | `endpoint` is the route template, `unmatched` when no route matched |
| `http_request_duration_seconds` | both | histogram, same labels minus `status` |
| `signals_generated_total{direction,strategy,symbol}` | signal-service | one per generated signal |
| `bot_engine_ws_clients_connected` | bot-engine | gauge |
| `bot_engine_kill_switch_engaged` | bot-engine | 1 while orders are blocked; alert on it |

signal-service runs one uvicorn worker: Prometheus counters are per process, so several
workers would make every scrape read a different process. Scale with replicas.
