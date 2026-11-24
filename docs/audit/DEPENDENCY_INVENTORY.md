# Dependency Inventory — M0

Versions are as declared in manifests. "Used?" means the dependency is imported by code that runs at runtime, verified by grep.

## 1. signal-service (Python)

Two manifests disagree (TD-08). `Dockerfile:14` installs from `requirements.txt`; README, Makefile, and CI install from `pyproject.toml`.

| Package | pyproject.toml | requirements.txt | Used? | Notes |
|---|---|---|---|---|
| fastapi | ≥0.111.0 | ≥0.111.0 | Yes | |
| uvicorn[standard] | ≥0.29.0 | ≥0.29.0 | Yes | |
| pydantic | ≥2.7.0 | ≥2.7.0 | Yes | |
| pydantic-settings | **missing** | ≥2.3.0 | Yes (`config.py:6`) | Install via pyproject breaks at import |
| pandas | ≥2.2.0 | ≥2.2.0 | Yes | |
| pandas-ta | ≥0.3.14b | — | **No** (removed in `dcebba7`) | Stale entry |
| numpy | ≥1.26.0 | ≥1.26.0 | Indirect (tests) | |
| structlog | ≥24.1.0 | ≥24.1.0 | Yes | |
| httpx | ≥0.27.0 | ≥0.27.0 | Healthcheck only | |
| alpaca-py | ≥0.25.0 | ≥0.25.0 | Yes (market data only) | Sync client used in async code (TD-13) |
| opentelemetry-api / sdk | ≥1.24.0 | ≥1.24.0 | **No** | Installed, never configured |
| opentelemetry-instrumentation-fastapi | ≥0.44b0 | ≥0.44b0 | **No** | Never instrumented |
| prometheus-client | ≥0.20.0 | ≥0.20.0 | Yes | |
| pytest, pytest-asyncio, pytest-cov, ruff, mypy | dev extra | **runtime list** | Dev | Shipped in the runtime image |

There is no lock file and no upper bounds.

## 2. bot-engine (Go 1.22)

Direct requirements (`bot-engine/go.mod:5-15`), pinned by `go.sum`:

| Module | Version | Used? |
|---|---|---|
| github.com/alpacahq/alpaca-trade-api-go/v3 | v3.4.0 | Yes (trading API) |
| github.com/gin-gonic/gin | v1.10.0 | Yes |
| github.com/google/uuid | v1.6.0 | Yes |
| github.com/gorilla/websocket | v1.5.1 | Yes |
| github.com/prometheus/client_golang | v1.19.1 | Yes |
| github.com/shopspring/decimal | v1.3.1 | Adapter only (domain uses float64, TD-21) |
| github.com/stretchr/testify | v1.9.0 | Tests |
| go.opentelemetry.io/otel | v1.24.0 | API only, no SDK/exporter (TD-14) |
| go.uber.org/zap | v1.27.0 | Yes |

Absent despite architecture docs: an AMQP client, a Postgres driver, and a Redis client.

## 3. app (Flutter)

`app/pubspec.yaml` (Dart SDK `>=3.3.0 <4.0.0`). The lock file is git-ignored (TD-18).

| Package | Constraint | Used? |
|---|---|---|
| flutter_bloc / bloc | ^8.1.5 / ^8.1.4 | Yes |
| equatable | ^2.0.5 | Yes |
| dartz | ^0.10.1 | Yes (Either) |
| get_it | ^7.7.0 | Yes |
| http | ^1.2.1 | Yes |
| web_socket_channel | ^2.4.5 | Yes |
| fl_chart | ^0.67.0 | Yes (sparkline) |
| intl | ^0.19.0 | Yes |
| dev: bloc_test, mocktail, flutter_lints | ^9.1.7, ^1.0.4, ^3.0.2 | Tests |

## 4. Container images

| Image | Tag | Pinned? | Used by code? |
|---|---|---|---|
| golang | 1.22-alpine (build) | Minor | — |
| alpine | 3.20 | Minor | — |
| python | 3.11-slim | Minor | — |
| postgres | 16-alpine | Major | **No** |
| timescale/timescaledb | latest-pg16 | **No** | **No** |
| redis | 7-alpine | Major | **No** |
| rabbitmq | 3-management-alpine | Major | **No** |
| prom/prometheus | v2.52.0 | Yes | Yes |
| grafana/grafana | 11.0.0 | Yes | Yes |
| grafana/loki | 3.0.0 | Yes | Yes |
| grafana/promtail | 3.0.0 | Yes | Yes |
| jaegertracing/all-in-one | 1.57 | Yes | **Receives no data** |

## 5. External services

| Service | Used by | Purpose | Credentials |
|---|---|---|---|
| Alpaca Market Data API | signal-service | OHLCV bars | `ALPACA_API_KEY` / `ALPACA_SECRET_KEY` |
| Alpaca Trading API (paper) | bot-engine | Orders, account, positions, clock | Same keys; host from `ALPACA_BASE_URL` (unguarded, TD-02) |
| GitHub Actions / GHCR | CI | Build and push | `GITHUB_TOKEN`; account currently billing-locked |
| Kubernetes cluster | CI deploy jobs | Deploy | `KUBECONFIG` secret; no cluster known to exist |
| Fly.io | Makefile / `fly.toml` | Deploy | flyctl auth; app names inconsistent (TD-15) |
| Codecov | signal-service CI | Coverage upload | — |
