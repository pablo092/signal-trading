# Technical Debt Register — M0

Priorities:

- **P0**: unsafe; must be fixed before any execution-capable or agent-reachable deployment.
- **P1**: broken or incorrect behavior, or a missing quality gate.
- **P2**: significant gap or inconsistency.
- **P3**: hygiene.

Effort: **S** ≤ 1 day, **M** 2–4 days, **L** ≥ 1 week.

"Verified" means confirmed by reading code or running a command. "Predicted" means derived from static analysis but not executed.

| ID | P | Title | Effort | Blocks M1 (read-only)? |
|---|---|---|---|---|
| TD-01 | P0 | Unauthenticated order placement endpoint | M | **Yes** |
| TD-02 | P0 | Paper-trading mode not enforced | S | **Yes** |
| TD-03 | P0 | No order idempotency | M | No (blocks execution) |
| TD-04 | P0 | Risk controls stubbed or unused | M | No (blocks execution) |
| TD-05 | P0 | No kill switch, no durable order/audit store | L | No (blocks execution) |
| TD-06 | P1 | Incorrect order state machine and fill handling | M | No |
| TD-07 | P1 | Python test suite out of sync with code | S | **Yes** |
| TD-08 | P1 | Python packaging and image dependencies broken | S | **Yes** |
| TD-09 | P1 | CI never executed; signal-service workflow paths wrong | S | **Yes** |
| TD-10 | P1 | Documented architecture ≠ implemented architecture | S (docs) | No |
| TD-11 | P1 | WebSocket hub concurrency and identity defects | M | No |
| TD-12 | P1 | WebSocket contract mismatch app ↔ engine | S | No |
| TD-13 | P1 | Blocking, timeout-less market-data calls | S | **Yes** |
| TD-14 | P2 | Observability gaps (tracing no-op, dead metrics) | M | No |
| TD-15 | P2 | Duplicate/stale config and docs | S | No |
| TD-16 | P2 | Infrastructure ports and default credentials | S | No |
| TD-17 | P2 | K8s/Fly manifests inconsistent with safety assumptions | M | No |
| TD-18 | P2 | Flutter app mostly mock; no order/portfolio data layer | M | No |
| TD-19 | P2 | Internal error details returned to clients | S | No |
| TD-20 | P2 | signal-service open: no auth, CORS `*`, no rate limit | S | Partial (needed before agent HTTP transport) |
| TD-21 | P3 | Monetary values as `float64` in Go | M | No |
| TD-22 | P3 | Go module path and weak port typing | S | No |
| TD-23 | P3 | Thin test coverage, no contract tests | M | No |
| TD-24 | P3 | Timeout budget and unused circuit breaker | S | No |

---

## P0

### TD-01 — Unauthenticated order placement endpoint
- **Description:** `POST /api/v1/orders/place` fetches a signal and submits a real broker order with no authentication or authorization. The same applies to `/ws` and the portfolio endpoints.
- **Evidence (verified):** `bot-engine/main.go:116-118` registers the routes with no auth middleware. `bot-engine/internal/infrastructure/http/handlers/orders.go:26`. Port `8080` is published on all host interfaces (`docker-compose.yml:78-79`). `bot-engine/fly.toml:21-32` exposes it publicly on 80/443.
- **Affected files:** `bot-engine/main.go`, `bot-engine/internal/infrastructure/http/handlers/*.go`, `docker-compose.yml`, `bot-engine/fly.toml`.
- **Business impact:** Anyone who can reach the port can trade the account, including a misconfigured agent or a browser page (CORS is irrelevant for simple POSTs from scripts).
- **Recommendation:**
  - Add service authentication (API key/JWT middleware) with scopes: `orders:write` separate from `portfolio:read`.
  - Bind to `127.0.0.1` in Compose.
  - Make the order route disabled by default via a feature flag (`ORDER_EXECUTION_ENABLED=false`).
- **Dependencies:** ADR-003, ADR-005.

### TD-02 — Paper-trading mode not enforced
- **Description:** `PAPER_TRADING` is parsed and logged but never gates anything. The Alpaca client connects to whatever `ALPACA_BASE_URL` says. `/portfolio/equity` always reports `paper_mode: true`.
- **Evidence (verified):** `bot-engine/internal/infrastructure/config/config.go:30-33`; `bot-engine/internal/infrastructure/broker/alpaca.go:23-28`; `bot-engine/main.go:51` (log only); `handlers/portfolio.go:57` (hardcoded).
- **Business impact:** Setting a live URL (or live keys plus a live URL) silently trades real money, while the UI and API still claim paper mode.
- **Recommendation:**
  - Fail startup unless `PAPER_TRADING=true` and the base URL host is exactly `paper-api.alpaca.markets`.
  - Live mode must require a separate explicit flag (`LIVE_TRADING_ACKNOWLEDGED=<date>`) plus an ADR.
  - Report the *actual* mode in `/portfolio/equity`.
- **Dependencies:** ADR-005.

### TD-03 — No order idempotency
- **Description:** Every call creates a new order UUID and a new broker order. The signal ID is not deduplicated, and no `client_order_id` is sent to Alpaca. The code comment claims the opposite.
- **Evidence (verified):** `bot-engine/internal/application/usecases/place_order.go:71` ("Idempotent — safe to retry"); `place_order.go:143` (`NewMarketOrder` → `uuid.New()` in `entities/order.go:72`); `broker/alpaca.go:36-42` (no `ClientOrderID`).
- **Business impact:** Client retries, double clicks, or agent loops produce duplicate positions.
- **Recommendation:** Require an `Idempotency-Key` header. Persist `(key → order)` with a unique constraint, pass it as Alpaca `client_order_id`, and reject a second order for the same `signal_id`.
- **Dependencies:** TD-05 (durable store), ADR-005.

### TD-04 — Risk controls stubbed or unused
- **Description:**
  - The daily loss check always sees `0` PnL.
  - `MaxOpenPositions`, `StopLossPct`, and `TakeProfitPct` are never applied.
  - `RiskRules.Validate()` is never called.
  - Market hours are not checked.
  - A SELL signal is executed without checking for an existing long position, so it can open a short, sized from total equity.
- **Evidence (verified):** `place_order.go:111-126` and `185-188` (`getDailyPnL` returns `0, nil`); `place_order.go:139-142` (SELL mapping); `place_order.go:129-133` (sizing from equity for both sides); `entities/risk_rules.go:30-47` (Validate, unused); `IsMarketOpen` is only used by the portfolio handler.
- **Business impact:** The README's risk table is not true; losses are unbounded per day and per position count.
- **Recommendation:** Move to a dedicated `RiskEngine` with a pre-trade check list and fail-closed semantics: if any input (PnL, positions, clock) is unavailable, reject. Add bracket orders for SL/TP. Allow SELL only for held quantity unless shorting is explicitly enabled.
- **Dependencies:** TD-05, ADR-005.

### TD-05 — No kill switch, no durable order/audit store
- **Description:** Orders live in a process-local map. There is no audit record of who requested what, no trade table writes, and no emergency stop.
- **Evidence (verified):** `bot-engine/main.go:66-67` (`NewInMemoryOrderRepo`); `infra/postgres/init.sql` defines `orders`/`trades`, but nothing in the repo writes to them (no DB driver in `go.mod`).
- **Business impact:** A restart loses all state. Incidents cannot be reconstructed, and there is no way to halt trading quickly.
- **Recommendation:**
  - Implement a Postgres order repository with migrations, and an append-only `audit_events` table (actor, action, payload hash, result).
  - Add a kill switch: a persisted flag checked before every submission, an admin endpoint plus env override, and a cancel-all-open-orders routine.
- **Dependencies:** migration tooling decision, ADR-005.

## P1

### TD-06 — Incorrect order state machine and fill handling
- **Evidence (verified):** `place_order.go:162-163`. `MarkFilled(brokerID, signal.Price)` sets `FilledAt`/`FilledPrice` to the *signal* price, then `Transition(Submitted)` overrides the status. The resulting order is "submitted" but carries fill data. `broker/alpaca.go:81-83` returns `FilledQty` where `ports.go:41-42` documents a fill *price*. `OrderFilled` is subscribed (`main.go:63`) but never published.
- **Impact:** Reported fills and PnL are fictional, and positions cannot be reconciled.
- **Recommendation:** Set status to `submitted` after broker acceptance. Reconcile fills via the Alpaca trade-updates stream or polling, and publish `OrderFilled` with the actual fill price and quantity. Fix the port signature.
- **Effort:** M. **Depends on:** TD-05.

### TD-07 — Python test suite out of sync with code
- **Evidence (predicted from static analysis; not executed, see TEST_COVERAGE.md):** 11 of 30 tests in `signal-service/tests/test_strategies.py` call APIs that do not exist:
  - `make_bars(trend=…, noise=…)` (lines 78, 100, 171) — `conftest.py:10` accepts `n, base_price, seed` only.
  - `MovingAverageCrossStrategy(ma_type=…)` (lines 129, 143, 176) — not a constructor parameter (`ma_cross_strategy.py:28`).
  - `StrategyFactory.register` (line 245) — does not exist.
  - `factory.available_strategies()` (line 235) — it is a property.
  - Case-insensitive lookup (line 217) — registry is case-sensitive.
  - Error message containing `"rsi"` (line 231) — message does not list strategies.
  - `"Missing required columns"` (line 56) — `validate_bars` only checks length (`ports.py:50-58`).
- **Impact:** No reliable regression net for the service M1 will extend.
- **Recommendation:** Decide per test whether the feature should exist (SMA option, `register`) or the test should change. Add adapter/route tests with a fake market-data port.
- **Effort:** S.

### TD-08 — Python packaging and image dependencies broken
- **Evidence:**
  - `signal-service/pyproject.toml:3`: `build-backend = "setuptools.backends.legacy:build"`. This is not a standard setuptools backend (expected `setuptools.build_meta`), so `pip install -e ".[dev]"` (used by README, Makefile, CI) is expected to fail. **Predicted; verify on Python 3.11 with current setuptools.** Local check: `import setuptools.backends` fails on setuptools 63.2.0, while `setuptools.build_meta` imports fine.
  - `pyproject.toml:15` still lists `pandas-ta`, which commit `dcebba7` removed from code and from `requirements.txt`.
  - `pyproject.toml` lacks `pydantic-settings`, which is required by `src/infrastructure/config.py:6`.
  - `requirements.txt:18-23` includes pytest/ruff/mypy, and `Dockerfile:14` installs them into the runtime image.
- **Impact:** Two dependency sources disagree, installs are not reproducible, and the image is larger with a wider attack surface.
- **Recommendation:** Make `pyproject.toml` the single source of truth (runtime vs `dev` extra). Fix the backend, generate a lock file (`uv`/`pip-tools`), and install only runtime deps in the image.
- **Effort:** S.

### TD-09 — CI never executed; signal-service workflow paths wrong
- **Evidence (verified):** `gh run list` shows 4 runs, all failed in 3–6 s. The annotation reads "The job was not started because your account is locked due to a billing issue." `.github/workflows/signal-service.yml:7,12,28,59,88,96,137-138` use `services/signal-service/**`, which does not exist, so the workflow never triggers on service changes. Both workflows contain production deploy jobs to Kubernetes that depend on a `KUBECONFIG` secret and on manifests with placeholder images.
- **Impact:** No automated gate has ever validated any commit.
- **Recommendation:**
  - Resolve the billing lock, or agree on a local `make ci` target.
  - Fix the paths.
  - Remove or disable the deploy jobs until an environment exists.
  - Add a Flutter job.
- **Effort:** S.

### TD-10 — Documented architecture ≠ implemented architecture
- **Evidence (verified):** No AMQP, SQL, or Redis client libraries exist in `bot-engine/go.mod` or in signal-service dependencies, and no code references `REDIS_URL`/`DATABASE_URL` beyond config parsing (`signal-service/src/infrastructure/config.py:32-36`). The README data flow (`README.md:16-22`) shows RabbitMQ; the implemented flow is synchronous HTTP (`signalclient/client.go:52-102`).
- **Impact:** Planning built on the README will be wrong. Compose runs four unused stateful services.
- **Recommendation:** Update the README to match [CURRENT_ARCHITECTURE.md](../architecture/CURRENT_ARCHITECTURE.md). Move unused infrastructure behind a Compose `profiles: [full]` until it is used.
- **Effort:** S.

### TD-11 — WebSocket hub concurrency and identity defects
- **Evidence (verified by code reading):**
  - `bot-engine/internal/infrastructure/messaging/ws_hub.go:154-163`: `ping()` calls `conn.WriteMessage` while `writePump` (`:120-128`) writes concurrently. gorilla/websocket supports a single concurrent writer only, so this is a data race and a potential panic.
  - `:73-75`: clients are keyed by the user-supplied `client_id` (default `"anon"`, `main.go:108-111`). A second connection with the same ID replaces the first in the map, and the first's `readPump` cleanup (`:131-134`) then deletes the *new* client.
  - `:21`: `CheckOrigin` returns `true`; the endpoint is unauthenticated.
- **Impact:** Crashes or silent loss of live updates; any web origin can subscribe to order events.
- **Recommendation:** Route pings through `writePump` (ticker inside the pump), key clients by a server-generated ID, authenticate the upgrade, and use an origin allowlist. Add `go test -race` WS tests.
- **Effort:** M.

### TD-12 — WebSocket contract mismatch app ↔ engine
- **Evidence (verified):**
  - `app/lib/features/signals/data/datasources/signal_remote_data_source.dart:57-59` accepts only `{"type":"signal","data":{…}}`.
  - The engine publishes `{"type":"OrderPlaced","payload":<Order>}` (`ws_hub.go:88-97`). `entities.Order` has no JSON tags (`entities/order.go:42-57`), so fields serialize as `ID`, `Symbol`, and so on.
  - The engine never publishes signals.
- **Impact:** The dashboard's live stream never receives anything.
- **Recommendation:** Define a versioned event schema (JSON Schema/AsyncAPI) shared by both sides; add a DTO layer in Go; add a contract test.
- **Effort:** S.

### TD-13 — Blocking, timeout-less market-data calls
- **Evidence (verified by code reading):** `signal-service/src/infrastructure/adapters/alpaca_market_data.py:44-69`. The `async def get_bars` calls the synchronous `StockHistoricalDataClient.get_stock_bars` directly, which blocks the event loop with no timeout. `StockBarsRequest` sets `limit` but no `start`. **To verify:** whether Alpaca's default window returns enough bars for intraday timeframes (risk of `InsufficientDataError` outside market hours).
- **Impact:** One slow Alpaca call stalls all requests in that worker. Agent-driven query volume will amplify this.
- **Recommendation:** Wrap the call in `asyncio.to_thread` or use an async HTTP client with an explicit timeout. Compute `start` from timeframe × bars. Add a TTL cache, since Redis is already deployed.
- **Effort:** S.

## P2

### TD-14 — Observability gaps
- No OTel `TracerProvider`/exporter in either service, so spans are no-ops. `JAEGER_ENDPOINT` is unused (`signal-service/src/infrastructure/config.py:28`; `bot-engine/internal/infrastructure/middleware/middleware.go:62-73`).
- `signals_generated_total` is declared but never incremented (`signal-service/src/main.py:50-54`), so 2 Grafana panels are always empty.
- The Grafana panel "WS Clients Connected" queries `bot_engine_ws_clients_connected`, which is never registered.
- Go metrics label `status` with `http.StatusText` (`middleware.go:99`), so `5..` regexes cannot match.
- `uvicorn --workers 2` (`Dockerfile:41`) with the default prometheus registry means each scrape sees one random worker.
- **Recommendation:** Configure OTLP exporters to Jaeger (OTLP is already enabled, `docker-compose.yml:262`), fix the metrics, and use prometheus multiprocess mode or a single worker. **Effort:** M.

### TD-15 — Duplicate/stale config and docs
- `signal-service/docker-compose.yml` duplicates the root compose: it uses `./services/signal-service`, `version: "3.9"`, and lacks bot-engine.
- `signal-service/README.md` references `services/signal-service`, `infra/kubernetes/`, Riverpod, and alerts that don't exist.
- The root `README.md:41-42` quick start runs the stale compose.
- `README.md:28` still says pandas-ta.
- `.gitignore:3` (`.env.*`) ignores `.env.example`, so a new clone has no template although Makefile and README require one (`Makefile:102`).
- `Makefile:106,110` set secrets on Fly apps `signal-service`/`bot-engine`, but `fly.toml` names them `trading-bot-signal-service`/`trading-bot-engine`.
- **Recommendation:** Delete the duplicate compose (after approval), add a `!.env.example` exception, and align names and docs. **Effort:** S.

### TD-16 — Infrastructure ports and default credentials
- Postgres 5432, TimescaleDB 5433, Redis 6379 (no password), Loki 3100, Prometheus 9090 (with `--web.enable-lifecycle`, which allows remote shutdown/reload), and Jaeger collector ports are all published on `0.0.0.0` (`docker-compose.yml:116-117,137-138,155-156,199-201,236-237,263-268`).
- Fallback credentials are `postgres/postgres`, `rabbit/rabbit`, and `admin/admin` (`docker-compose.yml:111,171-172,211-212`), and the Makefile prints them (`Makefile:24,26`).
- `timescale/timescaledb:latest-pg16` is unpinned (`:128`).
- Promtail mounts `docker.sock` (`:248`).
- **Recommendation:** Bind to `127.0.0.1` or remove host ports for internal services, require passwords without fallbacks, pin images, and add Redis `requirepass`. **Effort:** S.

### TD-17 — K8s/Fly manifests inconsistent with safety assumptions
- `bot-engine/bot-engine.yml:51` says "Single replica — order idempotency is critical", yet the HPA allows 3 replicas (`:143-144`) and `maxSurge: 1` (`:59`), with an in-memory repository. That is duplicate execution plus split state.
- Images are placeholders (`ghcr.io/REPLACE_ORG/…`, `ghcr.io/your-org/…`).
- `DATABASE_URL` with credentials sits in a ConfigMap (`signal-service/signal-service.yml:27`).
- No manifests exist for Postgres/Redis/Jaeger services referenced by the ConfigMaps, and there is no NetworkPolicy.
- `bot-engine/fly.toml:30-40` declares port 443 twice.
- **Recommendation:** Mark the manifests as non-deployable until reviewed, fix replicas to 1 with `strategy: Recreate` for the engine, and move credentials to Secrets. **Effort:** M.

### TD-18 — Flutter app mostly mock
- Portfolio values are hardcoded (`dashboard_page.dart:146-150`), the "Place Order" button just closes the dialog (`:370-371`), there are no portfolio/orders data sources, and only the `web/` platform is tracked.
- The WS reconnect loop retries every 3 s forever, including after `dispose()` (`signal_remote_data_source.dart:64-71`).
- `pubspec.lock` is git-ignored (`.gitignore:41`), so app builds are not reproducible.
- **Recommendation:** Implement portfolio/orders read paths; keep order placement out of the app until ADR-005 is implemented. **Effort:** M.

### TD-19 — Internal error details returned to clients
- `orders.go:75`, `portfolio.go:49,84` return `err.Error()`, which can include Alpaca error bodies. `signals.py:121-144` returns `str(exc)`.
- **Recommendation:** Return stable error codes plus a correlation ID; log the details server-side. **Effort:** S.

### TD-20 — signal-service open to any caller
- No auth, CORS `allow_origins=["*"]` (`signal-service/src/main.py:105-110`), no rate limiting. Each `/generate` call triggers an Alpaca data request, so quota can be exhausted by any caller.
- **Recommendation:** Add service-to-service auth before exposing it to an agent transport over HTTP, plus rate limiting and caching (TD-13). **Effort:** S.

## P3

### TD-21 — Money as `float64` in Go
`entities/order.go:47-52`, `risk_rules.go`, and `ports.Signal.Price` use `float64`, while Python uses `Decimal` (`price.py`). Use `shopspring/decimal` (already a dependency) in the domain. **Effort:** M.

### TD-22 — Module path and port typing
`bot-engine/go.mod:1` uses `github.com/trading-bot/bot-engine`, unrelated to the repo. `OrderRepositoryPort.FindByID(ctx, id interface{})` (`ports.go:54`) is untyped. **Effort:** S.

### TD-23 — Thin coverage, no contract tests
Go has tests in 2/13 packages; handlers, broker, WS, and signal client are at 0%. Python covers strategies only. There are no contract tests for signal-service ↔ bot-engine (`signalclient/client.go:19-30` mirrors the schema by hand) or engine ↔ app. **Effort:** M.

### TD-24 — Timeout budget and unused circuit breaker
The HTTP server `WriteTimeout` is 15 s (`main.go:125`). The order path chains signal-service (15 s timeout, `client.go:44`), then Alpaca account (no explicit timeout), then Alpaca order, so it can exceed the server budget mid-submission. `circuitbreaker` is implemented and tested but not wired. Alpaca SDK calls ignore `ctx`. **Effort:** S.
