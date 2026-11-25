# Contracts (BL-16)

JSON Schemas (Draft 2020-12) for the payloads that cross service boundaries. Each producer
and each consumer tests against the same files, so a change that breaks the other side fails
a test on both sides.

| Schema | Payload | Producer | Consumers |
|---|---|---|---|
| `signal.schema.json` | `POST /api/v1/signals/generate` response | signal-service | bot-engine, agent-gateway, app |
| `order.schema.json` | an order in `/api/v1/orders*` and in `order.placed` events | bot-engine | agent-gateway, app |
| `kill-switch.schema.json` | `/api/v1/admin/kill-switch` response | bot-engine | operators, app |
| `portfolio-equity.schema.json`, `portfolio-positions.schema.json` | `/api/v1/portfolio/*` responses | bot-engine | agent-gateway, app |
| `error.schema.json` | the error envelope of both services | both | all |
| `ws-event.schema.json` | `/ws` messages: envelope `{v, type, id, ts, data}` | bot-engine | app |

`examples/` holds one valid document per schema; `examples/manifest.json` maps each example
to its schema. Tests validate the examples and the real responses.

| Where | Test |
|---|---|
| bot-engine | `internal/contracttest` (examples and known-bad documents), `http/server/contract_test.go` (live responses), `messaging/contract_test.go` (WS events), `signalclient` (decodes the signal example) |
| signal-service | `tests/test_contracts.py` |
| agent-gateway | `tests/test_contracts.py` (reference `jsonschema` validator) |
| app | `test/contract_test.dart` (decodes the examples) |

## Rules

- Money and quantities are JSON numbers written from decimal strings: never compute them in float.
- WebSocket envelopes carry `v`. Additive changes (new optional fields, new event types) keep
  `v: 1`, and clients ignore unknown types. A breaking change bumps `v`, and clients drop
  versions they do not know.
- Change the schema, the example and both sides in the same PR.

The Go and signal-service validators implement the subset of JSON Schema these files use;
agent-gateway checks the same files with the full `jsonschema` library, so a schema that
relies on an unsupported keyword is caught there.
