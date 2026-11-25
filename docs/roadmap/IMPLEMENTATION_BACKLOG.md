# Implementation Backlog

Prioritized from [TECHNICAL_DEBT.md](../audit/TECHNICAL_DEBT.md) and the ADRs. Effort: S ≤ 1 day, M 2–4 days, L ≥ 1 week. Order within a milestone is the suggested execution order.

## M1: Safe read-only foundation and agent market analysis

Exit criteria:
- An MCP client can list strategies, generate signals, and read the portfolio and the *verified* trading mode.
- No path exists from any agent or unauthenticated caller to order submission.
- CI (or the agreed local gate) is green.

| ID | Item | Fixes | Effort | Depends on | Acceptance |
|---|---|---|---|---|---|
| BL-01 ✅ | Restore a working quality gate: resolve the GitHub billing lock **or** add `make ci`; fix `services/signal-service` paths; disable deploy jobs | TD-09 | S | — | A PR touching `signal-service/` triggers its workflow; lint and tests run |
| BL-02 ✅ | Fix Python packaging: `pyproject.toml` as single source, valid build backend, add `pydantic-settings`, drop `pandas-ta`, runtime-only image, lock file | TD-08 | S | — | `pip install -e ".[dev]"` succeeds on 3.11; image has no pytest/ruff/mypy |
| BL-03 ✅ | Reconcile the 11 out-of-sync strategy tests (fix tests or implement the missing features deliberately) | TD-07 | S | BL-02 | `pytest` green |
| BL-04 ✅ (93 %) | Route and adapter tests for signal-service with a fake `IMarketDataRepository` | TD-23 | S | BL-03 | Coverage ≥ 80 % on `src/` |
| BL-05 ✅ | Paper-mode guard at bot-engine startup and truthful `GET /api/v1/trading-mode` | TD-02, ADR-005 §5.1 | S | — | Startup fails with a non-paper host; unit tests cover the matrix |
| BL-06 ✅ | `ORDER_EXECUTION_ENABLED=false` by default; scoped service-token middleware in both services; bind host ports to `127.0.0.1` | TD-01, TD-20, ADR-003 | M | BL-05 | Order route returns 403 by default; requests without a token get 401; route-scope table test |
| BL-08 ✅ | Non-blocking Alpaca data calls with timeout, computed `start` window, TTL cache (in-process per worker instead of Redis: Redis is otherwise unused; swap behind the adapter if needed) | TD-13 | S | BL-02 | Concurrent requests not serialized (test); timeout returns 503 |
| BL-10 ✅ | `agent-gateway/` MCP server (Python, STDIO) with read-only tools from TARGET_ARCHITECTURE §4, an audit log line per call, and per-agent rate limit | ADR-002 | M | BL-06, BL-08 | Tool calls work from at least one MCP client; the gateway token is rejected on any write route |
| BL-11 ✅ | Align docs: README to current architecture; delete `signal-service/docker-compose.yml`; un-ignore and update `.env.example`; fix Makefile Fly app names | TD-10, TD-15 | S | — | README quick start works from a fresh clone |
| BL-13 ✅ | Compose hardening: no password fallbacks, pinned images, drop `--web.enable-lifecycle`, Redis password, unused infra moved to `profiles: [full]` | TD-16 | S | BL-11 | `docker compose up` starts only the needed services by default |
| BL-12 ✅ | Dependency scanning: `pip-audit`, `govulncheck`, Dart `pub outdated`/OSV in CI | SEC-13 | S | BL-01 | Scan job runs; findings triaged |
| BL-12a ✅ | Upgrade bot-engine to Go 1.26 and `golang.org/x/net` v0.60 / `x/text` v0.39 (9 reachable govulncheck findings, see docs/security/DEPENDENCY_SCAN.md) | SEC-13 | S | BL-12 | `make audit-go` clean |

## M2: Persistence, observability, and safety primitives

| ID | Item | Fixes | Effort | Depends on |
|---|---|---|---|---|
| BL-14 | Postgres order repository with migrations (e.g. `golang-migrate`) and append-only `audit_events` | TD-05 | L | BL-13 |
| BL-07 | Fake broker plus order-path handler and use-case integration tests | TD-23, ADR-004 | M | BL-18 |
| BL-09 ✅ | Risk-rules unit tests (sizing, SL/TP prices, `Validate`) before the refactor | TD-23 | S | — |
| BL-15 | WebSocket hub: single-writer pump, server-side client IDs, authenticated upgrade, origin allowlist, `-race` tests | TD-11, SEC-06 | M | BL-06 |
| BL-16 | `contracts/` (OpenAPI and WS event JSON Schema) plus contract tests in Go, Python, and Dart; fix app ↔ engine event mismatch | TD-12, ADR-001 | M | BL-15 |
| BL-17 | OTel OTLP exporters → Jaeger; increment `signals_generated_total`; register WS-clients gauge; numeric status labels; single-worker or multiprocess metrics | TD-14 | M | — |
| BL-18 | BrokerPort v2 (`ClientOrderID`, `GetOrderByClientID`, `CancelAllOrders`, capabilities, verified mode, ctx timeouts) and `decimal` money | ADR-004, TD-21, TD-24 | M | BL-05 |
| BL-19 | Kill switch (persisted flag, admin endpoint, env override, cancel-all, alert) | ADR-005 §5.7 | M | BL-14, BL-18 |
| BL-20 | Sanitized errors with codes and correlation IDs | TD-19 | S | — |

## M3: Order intents (execution-capable, paper only)

| ID | Item | Fixes | Effort | Depends on |
|---|---|---|---|---|
| BL-21 | Order-intent API, state machine, `Idempotency-Key`, mandatory human approval, TTL expiry; deprecate `/orders/place` | TD-03, ADR-005 §5.3–5.4 | L | BL-14, BL-18, BL-19 |
| BL-21a | Order path gaps found by BL-09: enforce `MaxOpenPositions` (validated but never checked) and fail closed when the daily-P&L check errors (today it continues) | TD-23 | S | BL-21 |
| BL-22 | Fail-closed risk engine with full checklist (ADR-005 §5.5), evaluated at creation and before submission | TD-04 | M | BL-21 |
| BL-23 | Fill reconciler via Alpaca trade updates; publish `OrderFilled`; correct the order state machine | TD-06 | M | BL-18 |
| BL-24 | Deployment manifests: engine `replicas: 1` / `Recreate` / no HPA, secrets out of ConfigMaps, real images, NetworkPolicy | TD-17 | M | — |
| BL-25 | Flutter: real portfolio/orders read paths; "Propose trade" via intents; approval screen (human scope) | TD-18 | M | BL-21 |
| BL-26 | MCP tool `create_order_intent` (agent scope `intent:create` only) | ADR-002, ADR-005 | S | BL-21, BL-22 |

## Later / requires new ADR

| ID | Item | Trigger |
|---|---|---|
| BL-27 | Reintroduce RabbitMQ for a concrete async flow (e.g. scheduled scans → intents) | A consumer exists |
| BL-28 | Consolidate TimescaleDB into the main Postgres when bar storage is needed | Backtesting or history feature |
| BL-29 | MCP Streamable HTTP transport with OAuth 2.1 | Remote agent access needed |
| BL-30 | Live trading | Separate ADR, after a paper track record and all M3 controls |
