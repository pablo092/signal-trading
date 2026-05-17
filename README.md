# Signal Trading Bot

A production-ready algorithmic trading system built with a microservices architecture.

## Architecture

```
signal-trading/
├── signal-service/     # Python — generates trading signals
├── bot-engine/         # Go — executes orders, manages risk
└── app/                # Flutter — mobile dashboard
```

### Data flow

```
Market Data (Alpaca) → signal-service → RabbitMQ → bot-engine → Alpaca Orders
                                                         ↓
                                                    WebSocket
                                                         ↓
                                                   Flutter App
```

## Tech Stack

| Layer | Technology |
|-------|-----------|
| Signal generation | Python 3.11, FastAPI, pandas-ta |
| Order execution | Go 1.22, Gin, OpenTelemetry |
| Mobile dashboard | Flutter 3, BLoC |
| Broker | Alpaca Markets (paper trading) |
| Databases | PostgreSQL + TimescaleDB, Redis |
| Messaging | RabbitMQ, WebSocket |
| Observability | Prometheus, Grafana, Loki, Jaeger |
| Infrastructure | Docker Compose, Kubernetes |

## Quick Start

```bash
# Start all services locally
cd signal-service
docker compose up -d

# The following will be available:
# signal-service:  http://localhost:8000/docs
# bot-engine:      http://localhost:8080/health/ready
# Grafana:         http://localhost:3000
# RabbitMQ UI:     http://localhost:15672
```

## Services

### signal-service (Python)
Generates trading signals using configurable strategies (RSI, MA Cross).

See [signal-service/README.md](signal-service/README.md) for full documentation.

### bot-engine (Go)
Receives signals, applies risk rules, and submits orders to Alpaca.
Exposes a WebSocket endpoint for real-time event streaming to the Flutter app.

### app (Flutter)
Mobile dashboard displaying live signals, portfolio state, and order history.

## Development

```bash
# signal-service
cd signal-service
pip install -e ".[dev]"
pytest --cov=src

# bot-engine
cd bot-engine
go test ./...

# app
cd app
flutter pub get
flutter test
```

## Risk Management

All orders are governed by configurable risk rules:

| Rule | Default |
|------|---------|
| Max position size | 5% of portfolio |
| Max daily loss | 2% |
| Stop loss | 3% |
| Take profit | 6% |
| Min signal confidence | 60% |
| Max open positions | 5 |
