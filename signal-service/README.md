# Trading Bot 🤖📈

Sistema de trading algorítmico con paper trading, generación de señales, monitoreo completo y despliegue en Kubernetes.

> ⚠️ **Este bot opera en modo paper trading por defecto (dinero simulado). Siempre validá resultados antes de activar dinero real.**

---

## Stack tecnológico

| Capa | Tecnología |
|---|---|
| Señales / Análisis | Python 3.11 · FastAPI · pandas-ta |
| Bot Engine | Go 1.22 · Gin · WebSocket |
| App móvil | Flutter 3.x · Riverpod |
| Broker | Alpaca Markets (paper trading) |
| Base de datos | PostgreSQL 16 · TimescaleDB · Redis |
| Mensajería | RabbitMQ |
| Observabilidad | Prometheus · Grafana · Loki · Jaeger |
| Infra | Docker Compose · Kubernetes · GitHub Actions |

---

## Arquitectura

```
Flutter App
    ↕  WebSocket + REST
Go Bot Engine  ←→  Python Signal Service  ←→  Alpaca API
    ↕                       ↕
PostgreSQL / Redis / TimescaleDB / RabbitMQ
    ↕
Prometheus → Grafana / Loki / Jaeger
```

**Patrones aplicados:**
- **Hexagonal Architecture** (Python) — Ports & Adapters
- **Clean Architecture** (Go) — Entities / Use Cases / Adapters
- **Strategy Pattern** — estrategias de señal intercambiables
- **Repository Pattern** — abstracción de fuentes de datos
- **Factory Pattern** — creación de estrategias por nombre
- **Observer / Event Bus** — propagación de señales
- **Circuit Breaker** — protección contra fallos del broker
- **Decorator Pattern** — logging, métricas y tracing transparentes
- **Command Pattern** — órdenes auditables con undo

---

## Setup local

### Prerequisitos
- Docker + Docker Compose v2
- Python 3.11+ (para desarrollo del signal-service)
- Go 1.22+ (para desarrollo del bot-engine)
- Flutter SDK (para la app)

### 1. Clonar y configurar credenciales

```bash
git clone https://github.com/your-org/trading-bot
cd trading-bot

# Crear cuenta gratuita en https://alpaca.markets (paper trading)
# Copiar las API keys en el .env
cp .env.example .env
nano .env
```

### 2. Levantar todo el stack

```bash
docker compose up -d
```

Servicios disponibles:
| Servicio | URL |
|---|---|
| Signal Service API | http://localhost:8000/docs |
| Bot Engine API | http://localhost:8080 |
| Grafana | http://localhost:3000 (admin/admin) |
| Prometheus | http://localhost:9090 |
| Jaeger UI | http://localhost:16686 |
| RabbitMQ UI | http://localhost:15672 (rabbit/rabbit) |

### 3. Probar el signal service

```bash
# Generar una señal RSI para AAPL
curl "http://localhost:8000/api/v1/signals/generate?symbol=AAPL&strategy=rsi"

# Listar estrategias disponibles
curl "http://localhost:8000/api/v1/signals/strategies"

# Health check
curl "http://localhost:8000/health/ready"
```

---

## Desarrollo

### Signal Service (Python)

```bash
cd services/signal-service

# Instalar dependencias con dev extras
pip install -e ".[dev]"

# Correr tests con cobertura
pytest

# Ver reporte HTML de cobertura
open htmlcov/index.html

# Lint
ruff check src/ tests/
ruff format src/ tests/

# Type check
mypy src/
```

### Variables de entorno requeridas

| Variable | Descripción | Ejemplo |
|---|---|---|
| `ALPACA_API_KEY` | API key de Alpaca | `PKxxxxxxxx` |
| `ALPACA_SECRET_KEY` | Secret key de Alpaca | `xxxxxxxx` |
| `PAPER_TRADING` | Usar paper trading | `true` |
| `LOG_LEVEL` | Nivel de logs | `INFO` |
| `ENVIRONMENT` | Entorno | `development` |

---

## Testing

### Cobertura mínima: 80% (enforced en CI)

```bash
# Signal Service
cd services/signal-service && pytest

# Con reporte detallado por módulo
pytest --cov-report=term-missing

# Fallar si cobertura < 80%
pytest --cov-fail-under=80
```

### Pirámide de tests

```
      ┌─────────────────┐
      │  E2E / Contract  │  10% — Pact, Flutter integration
      ├─────────────────┤
      │   Integration    │  20% — testcontainers, Alpaca sandbox
      ├─────────────────┤
      │   Unit Tests     │  70% — mocks, value objects, strategies
      └─────────────────┘
```

---

## Despliegue en Kubernetes

```bash
# Aplicar todos los manifests
kubectl apply -f infra/kubernetes/

# Verificar pods
kubectl get pods -n tradingbot

# Ver logs
kubectl logs -f deployment/signal-service -n tradingbot

# Escalar manualmente
kubectl scale deployment/signal-service --replicas=4 -n tradingbot
```

---

## Monitoreo

### Grafana Dashboards
- **Señales generadas** por estrategia, dirección, símbolo
- **Latencia** del análisis (p50, p95, p99)
- **Error rate** por tipo de error
- **Paper P&L** acumulado
- **Market data requests** — status codes, latencia broker

### Alertas Prometheus
- Latencia señales > 2s → alerta WARNING
- Error rate > 5% → alerta CRITICAL
- Pod restarts > 3 → alerta WARNING

---

## Estrategias disponibles

| Nombre | Descripción | Parámetros |
|---|---|---|
| `rsi` | RSI oversold/overbought | `period`, `oversold`, `overbought` |
| `ma_cross` | Golden/Death cross EMA/SMA | `fast_period`, `slow_period`, `ma_type` |

### Agregar una nueva estrategia

```python
# 1. Implementar ISignalStrategy
class MACDStrategy(ISignalStrategy):
    @property
    def name(self) -> str:
        return "MACDStrategy"

    def analyze(self, symbol, bars, timeframe) -> Signal:
        ...

# 2. Registrar en el factory
StrategyFactory.register("macd", MACDStrategy)

# 3. Usar via API
curl "localhost:8000/api/v1/signals/generate?symbol=AAPL&strategy=macd"
```

---

## Roadmap

- [x] Signal Service (Python) — RSI + MA Cross
- [x] Docker Compose stack completo
- [x] CI/CD con coverage gate
- [x] Kubernetes manifests
- [ ] Bot Engine (Go) — orden management
- [ ] Flutter App — dashboard de señales
- [ ] MACD Strategy
- [ ] Backtesting endpoint
- [ ] Paper trading dashboard en Grafana
- [ ] Notificaciones push (señales actionables)

---

## Licencia

MIT — Usá libremente, no nos hacemos responsables por pérdidas financieras.
