# System Components

Status: **Implemented** / **Partial** / **Absent** / **Proposed** (target only). Evidence paths are relative to the repo root.

## 1. Application components

| Component | Language | Status | Responsibility | Interfaces | Key files |
|---|---|---|---|---|---|
| signal-service | Python 3.11, FastAPI | Implemented | Fetch OHLCV bars, compute RSI / EMA-cross signals | REST `:8000` (`/api/v1/signals/generate`, `/api/v1/signals/strategies`, `/health/*`, `/metrics`, `/docs`) | `signal-service/src/main.py`, `src/api/routes/signals.py` |
| ├ StrategyFactory | Python | Implemented | Name → strategy (`rsi`, `ma_cross`) | in-process | `src/application/strategies/factory.py` |
| ├ AlpacaMarketDataAdapter | Python | Implemented (defects TD-13) | `IMarketDataRepository` over alpaca-py | Alpaca Data API | `src/infrastructure/adapters/alpaca_market_data.py` |
| bot-engine | Go 1.22, Gin | Implemented | Signal-driven order placement, portfolio read, WS events | REST `:8080` (`/api/v1/orders/place`, `/api/v1/portfolio/*`, `/ws`, `/health/*`, `/metrics`) | `bot-engine/main.go` |
| ├ PlaceOrderUseCase | Go | Partial (TD-03/04/06) | Signal → risk → order → persist → event | `ports.*` | `internal/application/usecases/place_order.go` |
| ├ RiskRules | Go | Partial (TD-04) | Sizing, confidence threshold | in-process | `internal/domain/entities/risk_rules.go` |
| ├ AlpacaBroker | Go | Implemented (TD-02) | `BrokerPort` over Alpaca trading API | Alpaca Trading API | `internal/infrastructure/broker/alpaca.go` |
| ├ InMemoryOrderRepo | Go | Partial | `OrderRepositoryPort` | in-process | `internal/infrastructure/repository/inmemory_order_repo.go` |
| ├ signalclient | Go | Implemented | `SignalServicePort` over HTTP | signal-service REST | `internal/infrastructure/signalclient/client.go` |
| ├ EventBus | Go | Implemented | In-process pub/sub | in-process | `internal/application/eventbus/eventbus.go` |
| ├ WSHub | Go | Implemented (TD-11) | Broadcast domain events | WebSocket | `internal/infrastructure/messaging/ws_hub.go` |
| ├ CircuitBreaker | Go | Partial (unwired) | Protect downstream calls | in-process | `internal/infrastructure/circuitbreaker/` |
| app | Dart/Flutter | Partial | Dashboard: signals (real), portfolio (mock) | REST to signal-service, WS to bot-engine | `app/lib/` |
| agent-gateway (MCP) | TBD (ADR-002) | **Proposed** | Expose read-only tools to agents | MCP STDIO / Streamable HTTP | — |
| Risk engine (dedicated) | Go | **Proposed** | Full, fail-closed pre-trade checks | in-process within bot-engine | — |
| Kill switch | Go | **Proposed** | Halt all execution | admin API + persisted flag | — |
| Fill reconciler | Go | **Proposed** | Sync order state from broker | Alpaca trade updates | — |
| Fake broker | Go | **Proposed** | Deterministic broker for tests and dry-run | `BrokerPort` | — |

## 2. Infrastructure components

| Component | Image | Status | Consumers | Host port |
|---|---|---|---|---|
| PostgreSQL | postgres:16-alpine | Deployed, unused | none | 5432 |
| TimescaleDB | timescale/timescaledb:latest-pg16 | Deployed, unused | none | 5433 |
| Redis | redis:7-alpine | Deployed, unused | none | 6379 |
| RabbitMQ | rabbitmq:3-management-alpine | Deployed, unused | none | 15672 (UI) |
| Prometheus | prom/prometheus:v2.52.0 | Implemented | scrapes both services | 9090 |
| Grafana | grafana/grafana:11.0.0 | Implemented | 1 dashboard | 3000 |
| Loki | grafana/loki:3.0.0 | Implemented | Promtail | 3100 |
| Promtail | grafana/promtail:3.0.0 | Implemented | Docker logs | — |
| Jaeger | jaegertracing/all-in-one:1.57 | Deployed, no data | none | 16686, 14268, 14250, 6831/udp, 9411 |

## 3. External dependencies

| Dependency | Used by | Criticality |
|---|---|---|
| Alpaca Market Data API | signal-service | Required for any signal |
| Alpaca Trading API (paper) | bot-engine | Required for orders and portfolio |
| GitHub Actions / GHCR | CI | Currently unavailable (billing lock) |

## 4. API surface (implemented)

| Method | Path | Service | Auth | Side effect |
|---|---|---|---|---|
| POST | `/api/v1/signals/generate` | signal-service | none | Alpaca data call |
| GET | `/api/v1/signals/strategies` | signal-service | none | — |
| POST | `/api/v1/orders/place` | bot-engine | **none** | **Broker order** |
| GET | `/api/v1/portfolio/equity` | bot-engine | none | Alpaca account and clock call |
| GET | `/api/v1/portfolio/positions` | bot-engine | none | Alpaca positions call |
| GET | `/ws` | bot-engine | none | Event subscription |
| GET | `/health/live`, `/health/ready`, `/metrics` | both | none | — |

OpenAPI: signal-service auto-generates `/openapi.json` (FastAPI). bot-engine has swag-style comments but no generated spec.
