# Signal Trading

Algorithmic signal platform for a **paper-trading** Alpaca account, built so AI agents can
analyze the market without being able to trade. Execution is disabled by default and live
trading is refused at startup.

## Architecture

```text
signal-trading/
├── signal-service/   # Python 3.11, FastAPI — computes signals from Alpaca market data
├── bot-engine/       # Go 1.22, Gin — portfolio, risk rules, WebSocket, (disabled) order path
├── agent-gateway/    # Python, MCP over STDIO — read-only tools for AI agents
├── app/              # Flutter — dashboard (web build deployed to Vercel)
├── api/              # Vercel functions — server-side proxy for the web demo
└── docs/             # audit, ADRs, roadmap, runbooks
```

```text
Alpaca market data ──► signal-service ◄── HTTP + token ── bot-engine ──► Alpaca (paper)
                            ▲                                 │
                            │ read-only tools                 └── WebSocket ──► Flutter app
                      agent-gateway (MCP) ◄── Claude Code / Codex
```

Services talk over HTTP with **scoped service tokens** (ADR-003): every `/api/v1/*` route
needs a token with the right scope, agent tokens can never hold write scopes, and an empty
token table denies everything. Order routes return 403 unless `ORDER_EXECUTION_ENABLED=true`
(ADR-005). See [docs/](docs/) for the audit, ADRs and the
[implementation backlog](docs/roadmap/IMPLEMENTATION_BACKLOG.md).

| Layer | Technology |
| --- | --- |
| Signals | Python 3.11, FastAPI, pandas, alpaca-py |
| Engine | Go 1.22, Gin, zap, Prometheus |
| Agent gateway | MCP Python SDK 2.x, httpx |
| Dashboard | Flutter 3, BLoC |
| Broker | Alpaca Markets, **paper only** |
| Deploy | Docker Compose (local), Fly.io (backends), Vercel (web) |

## Quick start (local)

Requirements: Docker with Compose v2, an Alpaca **paper** account.

```sh
cp .env.example .env
# 1. Fill ALPACA_API_KEY / ALPACA_SECRET_KEY with your PAPER keys.
# 2. Issue service tokens and fill SERVICE_AUTH_TOKENS / BOT_ENGINE_SIGNAL_TOKEN:
#    see docs/runbooks/SERVICE_TOKENS.md (without tokens every API call is 401).
docker compose up -d --build
```

| URL | |
| --- | --- |
| <http://127.0.0.1:8000/docs> | signal-service Swagger |
| <http://127.0.0.1:8000/health/ready> | signal-service readiness |
| <http://127.0.0.1:8080/health/ready> | bot-engine readiness |
| <http://127.0.0.1:8080/api/v1/trading-mode> | verified mode (token with `portfolio:read`) |

```sh
TOKEN=...   # a plaintext token from your table
curl -s -X POST http://127.0.0.1:8000/api/v1/signals/generate \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"symbol":"AAPL","strategy":"rsi"}'
```

The optional infrastructure (Postgres, TimescaleDB, Redis, RabbitMQ, Prometheus, Grafana,
Loki, Jaeger) is not used by the code yet. Start it with `docker compose --profile full up -d`
after setting its passwords in `.env`.

## Development

| Service | Setup | Checks |
| --- | --- | --- |
| signal-service | [signal-service/README.md](signal-service/README.md) (venv + hashed lockfile) | `make test-signal lint-signal` |
| bot-engine | Go 1.22 | `make test-engine lint-engine` |
| agent-gateway | [agent-gateway/README.md](agent-gateway/README.md) | `make test-gateway lint-gateway` |
| app | Flutter 3.41 | `cd app && flutter test` |

`make test`, `make lint` and `make audit` (dependency vulnerability scan) run everything.
GitHub Actions is currently unavailable (billing), so run them locally before a PR.

## Deployment

- **Backends on Fly.io** and the **web demo on Vercel**: [docs/runbooks/FLY_DEPLOY.md](docs/runbooks/FLY_DEPLOY.md).
- **AI agents** (Claude Code, Codex CLI) connect through [agent-gateway](agent-gateway/README.md).
- Tokens: [docs/runbooks/SERVICE_TOKENS.md](docs/runbooks/SERVICE_TOKENS.md).

## Safety

- Paper only: bot-engine refuses to start unless `PAPER_TRADING=true` and the broker host is
  exactly `paper-api.alpaca.markets`.
- `ORDER_EXECUTION_ENABLED=false` by default; agents can never hold an order scope.
- Signals are algorithmic indicators, not financial advice.

Risk rules applied by bot-engine to any order:

| Rule | Default |
| --- | --- |
| Max position size | 5% of portfolio |
| Max daily loss | 2% |
| Stop loss | 3% |
| Take profit | 6% |
| Min signal confidence | 60% |
| Max open positions | 5 |
