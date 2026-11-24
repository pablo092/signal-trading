# System Audit — M0

| Field | Value |
|---|---|
| Repository | `pablo092/signal-trading` |
| Branch | `chore/m0-technical-audit` |
| Base commit | `37c63ff` (main, 7 commits total) |
| Audit date | 2026-11-24 |
| Method | Full read of all 103 tracked files + safe command execution (no stack started, no broker calls) |

Status legend used in every M0 document:

| Label | Meaning |
|---|---|
| **Implemented** | Code exists, is wired at runtime and reachable |
| **Partial** | Code exists but is stubbed, unwired, or functionally incomplete |
| **Absent** | Described in docs/config but no code exists |
| **Not verified** | Could not be executed or checked in this environment |

Finding IDs (`TD-xx`) are defined in [TECHNICAL_DEBT.md](TECHNICAL_DEBT.md); security IDs (`SEC-xx`) in [SECURITY_AUDIT.md](SECURITY_AUDIT.md).

---

## 1. Executive summary

The repository is a **well-structured prototype**, not the "production-ready" system its README claims. Domain layering (Hexagonal in Python, Clean Architecture in Go) is clean and readable. Most of the advertised infrastructure, however, is **deployed but not used**: RabbitMQ, PostgreSQL, TimescaleDB, and Redis run in Compose, but no service code connects to them. The real runtime is two HTTP services: `bot-engine` calls `signal-service` synchronously, then calls Alpaca.

The most important finding concerns trading safety. `bot-engine` exposes an **unauthenticated `POST /api/v1/orders/place`** that submits broker orders. The service has **no idempotency** and **no enforcement of paper mode**: `PAPER_TRADING` is only logged, and `ALPACA_BASE_URL` alone decides the endpoint. Risk controls are **partially stubbed**: the daily-loss check always reads 0, and max positions, stop-loss, and take-profit are unused. This is acceptable for a local paper prototype. It is **not acceptable** as a base for agent-driven access.

Automated quality gates are also weaker than they look. **CI has never executed** (GitHub account billing lock). The signal-service workflow watches a path that does not exist. The Python test suite is out of sync with the code: 11 of 30 tests are predicted to fail.

**Recommendation: READY FOR M1 WITH BLOCKING ISSUES**, provided M1 is scoped to *read-only* market analysis. See §8.

## 2. Repository inventory

| Path | Contents | Notes |
|---|---|---|
| `signal-service/` | Python 3.11 FastAPI service (21 `.py` files), Dockerfile, `pyproject.toml`, `requirements.txt`, `fly.toml`, K8s manifest `signal-service.yml`, **duplicate** `docker-compose.yml`, README | The duplicate compose and README are stale (they reference `services/signal-service`) — TD-15 |
| `bot-engine/` | Go 1.22 Gin service (13 `.go` files), Dockerfile, `fly.toml`, K8s manifest `bot-engine.yml` | Module path `github.com/trading-bot/bot-engine` |
| `app/` | Flutter app (14 Dart files in `lib/`, 4 tests), `web/` platform only | No `android/` or `ios/` folders, despite the "mobile dashboard" claim |
| `infra/postgres/init.sql` | Schema: `signals`, `orders`, `trades` | No migration tool; nothing writes to these tables |
| `infra/monitoring/` | Prometheus, Grafana (provisioning + 1 dashboard), Loki, Promtail configs | No Alertmanager or alert rules (README claims alerts) |
| `docs/postman/` | 2 collections (15 requests) + environment | Environment contains URLs only, no secrets |
| `.github/workflows/` | `bot-engine.yml`, `signal-service.yml` | Never executed — TD-09 |
| Root | `docker-compose.yml` (11 services), `Makefile`, `README.md`, `.gitignore` | |

**Absent although referenced:** database migrations, RabbitMQ definitions (exchanges/queues), Alertmanager, `infra/kubernetes/` (referenced in the signal-service README), tracked `.env.example` (it exists locally but is git-ignored by `.env.*` — TD-15), OpenAPI spec for bot-engine (only Swagger-style comments, no generator).

Local untracked files: `.env` and `.env.example` exist. Only key names were inspected, never values. `.env` currently points `ALPACA_BASE_URL` to the **paper** host and `PAPER_TRADING=true`.

## 3. Service audit

### 3.1 signal-service (Python)

| Aspect | Status | Evidence |
|---|---|---|
| Entrypoint | Implemented | `src/main.py:83-150` app factory, `uvicorn --workers 2` (`Dockerfile:41`) |
| Routes | Implemented | `POST /api/v1/signals/generate`, `GET /api/v1/signals/strategies` (`src/api/routes/signals.py:62,147`), `/health/live`, `/health/ready`, `/metrics` |
| RSI strategy | Implemented | Native Wilder RSI (`src/application/strategies/rsi_strategy.py:124-132`) |
| MA Cross strategy | Implemented (EMA only) | `ma_cross_strategy.py:52-54`; the README/tests reference an `ma_type`/SMA option that does not exist |
| Market data (Alpaca) | Implemented, with defects | `alpaca_market_data.py:44-98`. A sync client is called inside `async def` (blocks the event loop), with no timeout — TD-13 |
| Alpaca trading | Absent (by design) | Market data only |
| RabbitMQ publisher | Absent | No AMQP dependency or code |
| Database / Redis | Absent | `REDIS_URL` and `DATABASE_URL` are read in `config.py:32-36` but never used |
| Config | Implemented | `pydantic-settings` (`config.py`), **missing from `pyproject.toml`** — TD-08 |
| Error handling | Implemented | Domain exceptions mapped to 422/503 (`signals.py:121-144`); raw `str(exc)` returned to client — TD-19 |
| Logging | Implemented | structlog JSON (`main.py:28-35`) |
| Metrics | Partial | HTTP counters work; `signals_generated_total` is declared (`main.py:50`) but never incremented — TD-14 |
| Tracing | Absent | OTel packages are installed, but no tracer provider/exporter is configured; `JAEGER_ENDPOINT` is unused — TD-14 |
| Auth | Absent | CORS `*` (`main.py:105-110`), no auth, no rate limit — TD-20 |
| Health | Partial | `/health/ready` does not check dependencies (Alpaca reachability) |
| Tests | **Not verified / predicted broken** | 30 tests, strategies only; 11 reference non-existent APIs — TD-07 |

### 3.2 bot-engine (Go)

| Aspect | Status | Evidence |
|---|---|---|
| Entrypoint / DI | Implemented | `main.go:32-156`, graceful shutdown |
| HTTP API | Implemented | `POST /api/v1/orders/place`, `GET /api/v1/portfolio/equity`, `GET /api/v1/portfolio/positions`, `/ws`, `/metrics`, health (`main.go:91-118`) |
| Order execution | Implemented (unsafe) | `PlaceOrderUseCase.Execute` (`place_order.go:72-183`) → Alpaca `PlaceOrder` (`alpaca.go:33-63`) |
| Risk management | **Partial** | Only `MinConfidence` and `MaxPositionSizePct` are enforced. `getDailyPnL` returns `0` (`place_order.go:185-188`). `MaxOpenPositions`, `StopLossPct`, and `TakeProfitPct` are unused, and `RiskRules.Validate()` is never called — TD-04 |
| Paper/live guard | **Absent** | `PaperTrading` is only logged (`main.go:51`); the broker uses `ALPACA_BASE_URL` unchecked (`config.go:33`, `alpaca.go:24-28`) — TD-02 |
| Idempotency | **Absent** | The comment claims "Idempotent" (`place_order.go:71`), but every call creates a new UUID and a new broker order — TD-03 |
| Order lifecycle | Partial / incorrect | `MarkFilled` then `Transition(Submitted)` (`place_order.go:162-163`); no fill reconciliation; `GetOrderStatus` returns filled qty as "price" (`alpaca.go:81-83`) — TD-06 |
| Persistence | Partial | In-memory repo only (`main.go:66-67`); orders are lost on restart — TD-05 |
| Portfolio | Implemented | Reads equity/positions from Alpaca; `paper_mode` is hardcoded `true` in the response (`portfolio.go:57`) |
| Broker abstraction | Implemented | `ports.BrokerPort` (`ports.go:36-49`); a single Alpaca adapter |
| Signal client | Implemented | Sync HTTP, 15 s timeout, no retry (`signalclient/client.go:40-48`) |
| Circuit breaker | Partial | Implemented and tested (97.4%), **not wired** anywhere — TD-24 |
| Event bus | Implemented | In-process; `OrderFilled` is subscribed but never published (`main.go:63`) |
| WebSocket | Implemented, with defects | Concurrent writers, `CheckOrigin: true`, unauthenticated, client-ID collisions (`ws_hub.go`) — TD-11 |
| RabbitMQ consumer | Absent | No AMQP dependency |
| DB / Redis | Absent | |
| Logging | Implemented | zap |
| Metrics | Implemented | HTTP metrics; the `status` label uses the status *text*, not the code (`middleware.go:99`) |
| Tracing | Partial | Spans are created, but no exporter/provider is configured, so they are no-ops — TD-14 |
| Kill switch / audit log | Absent | TD-05 |
| Tests | Implemented (thin) | 2/13 packages: usecases 61.8%, circuitbreaker 97.4%, rest 0% |

### 3.3 app (Flutter)

| Aspect | Status | Evidence |
|---|---|---|
| Entrypoint / DI | Implemented | `lib/main.dart`, `lib/injection_container.dart` (get_it) |
| State management | Implemented | BLoC (`signal_bloc.dart`); the signal-service README wrongly says Riverpod |
| Signals via REST | Implemented | `signal_remote_data_source.dart:26-42` → `POST /api/v1/signals/generate` |
| Live signals via WS | **Broken contract** | App expects `type == "signal"` and `data` (`signal_remote_data_source.dart:57-59`); engine emits `OrderPlaced` and `payload` with untagged Go structs — TD-12 |
| Portfolio screen | **Mock** | Hardcoded equity/PnL/positions (`dashboard_page.dart:146-150`) |
| Orders / Place order | **Mock** | The "Place Order" button only closes the dialog (`dashboard_page.dart:370-371`); no orders data source |
| Auth | Absent | |
| Platforms | web only | No `android/` or `ios/` folders tracked |
| Tests | Not verified | 4 test files exist; Flutter SDK is not installed locally |

## 4. Infrastructure audit (summary)

See [SECURITY_AUDIT.md](SECURITY_AUDIT.md) §4 and TD-16/17 for details.

| Component | In Compose | Used by code | Notes |
|---|---|---|---|
| PostgreSQL 16 | Yes (host port 5432) | **No** | `init.sql` schema unused |
| TimescaleDB | Yes (5433, `latest-pg16` tag) | **No** | No hypertables defined |
| Redis 7 | Yes (6379, no auth) | **No** | |
| RabbitMQ 3 | Yes (UI 15672; AMQP internal) | **No** | No definitions file |
| Prometheus | Yes (9090, `--web.enable-lifecycle`) | Scrapes both services | |
| Grafana | Yes (3000, default creds fallback) | Dashboard provisioned | 1 panel uses a non-existent metric |
| Loki + Promtail | Yes | Ships container logs | Promtail mounts `docker.sock` |
| Jaeger | Yes (several ports) | **Receives nothing** | No exporter in either service |

Dependency chain in Compose: `bot-engine` → `signal-service` (healthy) → `postgres` + `redis` (healthy). `signal-service` waits for Postgres/Redis without using them.

## 5. Test execution summary

See [TEST_COVERAGE.md](TEST_COVERAGE.md).

| Suite | Command | Result |
|---|---|---|
| Go tests | `go test ./... -count=1` | **PASS** (2 packages with tests) |
| Go vet | `go vet ./...` | **PASS** |
| Go build | `go build ./...` | **PASS** |
| Python tests | `python -m pytest` | **Not executed**: Python 3.10.8 < required 3.11, and fastapi/pydantic/pytest are not installed |
| Flutter | `flutter analyze` / `flutter test` | **Not executed**: Flutter SDK not installed |
| Compose | `docker compose config --quiet` | **PASS** (root); `signal-service/docker-compose.yml` passes with an obsolete-`version` warning |
| CI | `gh run list` | **Never ran**: all 4 runs failed with "account is locked due to a billing issue" |

## 6. Documentation vs. reality

| README claim | Reality |
|---|---|
| "production-ready" | Prototype; see TD-01…TD-06 |
| `Market Data → signal-service → RabbitMQ → bot-engine` | `bot-engine` pulls from `signal-service` over sync HTTP; no RabbitMQ |
| Databases: PostgreSQL + TimescaleDB, Redis | Deployed, unused |
| "Jaeger" observability | No traces are exported |
| Stop loss 3% / take profit 6% / max 5 positions / max daily loss 2% | Defined, not enforced |
| Quick start: `cd signal-service && docker compose up -d` | That compose file is stale and points to `./services/signal-service` |
| CI coverage gate 80% | CI never ran; Python coverage would be well below 80% (only strategies are tested) |
| Prometheus alerts (signal-service README) | No alert rules exist |

## 7. Main risks

**Technical:**
1. False confidence: docs describe a distributed, persistent system that does not exist.
2. No working quality gate (CI locked, Python tests broken, packaging likely broken).
3. Order state is in-memory and incorrect, so no reliable trade history exists.

**Security / trading safety:**
1. Unauthenticated order endpoint reachable from the host and from the public internet (Fly config).
2. Live trading is one env var away (`ALPACA_BASE_URL`), with no guard.
3. No idempotency, so retries or duplicate calls create duplicate orders.
4. No kill switch and no durable audit log.

## 8. Recommendation

**READY FOR M1 WITH BLOCKING ISSUES**, conditional on M1 being *read-only* (agent can query and analyze market data; no order capability).

Blocking issues for a read-only M1:

| ID | Why it blocks |
|---|---|
| TD-01 / TD-02 | Any agent tooling developed against the local stack must not be able to reach an unauthenticated order endpoint. A hard paper-only guard is also required. |
| TD-07 / TD-08 | M1 builds on signal-service; its tests and packaging must work first. |
| TD-09 | A working quality gate (repaired CI or an agreed local equivalent) is needed before adding code. |
| TD-13 | Market queries from an agent will block the event loop and may hang without a timeout. |

The rest of the backlog (persistence, idempotency, risk engine, kill switch) blocks any **execution-capable** milestone, not M1. See [IMPLEMENTATION_BACKLOG.md](../roadmap/IMPLEMENTATION_BACKLOG.md).
