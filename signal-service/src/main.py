"""Signal Service — FastAPI application entrypoint.

Swagger UI:  http://localhost:8000/docs
ReDoc:       http://localhost:8000/redoc
OpenAPI JSON: http://localhost:8000/openapi.json
Metrics:     http://localhost:8000/metrics
"""

from __future__ import annotations

import time
from contextlib import asynccontextmanager
from typing import AsyncIterator

import structlog
import uvicorn
from fastapi import FastAPI
from fastapi.middleware.cors import CORSMiddleware
from prometheus_client import Counter, Histogram, make_asgi_app
from starlette.middleware.base import BaseHTTPMiddleware
from starlette.requests import Request

from src.api.routes import signals as signals_router
from src.api.schemas import HealthResponse
from src.infrastructure.config import Settings

# ── Structured logging ───────────────────────────────────────────────────────
structlog.configure(
    processors=[
        structlog.contextvars.merge_contextvars,
        structlog.processors.add_log_level,
        structlog.processors.TimeStamper(fmt="iso"),
        structlog.processors.JSONRenderer(),
    ]
)
logger = structlog.get_logger(__name__)

# ── Prometheus metrics ───────────────────────────────────────────────────────
REQUEST_COUNT = Counter(
    "http_requests_total",
    "Total HTTP requests",
    ["method", "endpoint", "status"],
)
REQUEST_LATENCY = Histogram(
    "http_request_duration_seconds",
    "HTTP request latency",
    ["method", "endpoint"],
    buckets=[0.01, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0],
)
SIGNALS_GENERATED = Counter(
    "signals_generated_total",
    "Total signals generated",
    ["direction", "strategy", "symbol"],
)


class PrometheusMiddleware(BaseHTTPMiddleware):
    async def dispatch(self, request: Request, call_next):  # type: ignore[override]
        start = time.perf_counter()
        response = await call_next(request)
        duration = time.perf_counter() - start
        endpoint = request.url.path
        REQUEST_COUNT.labels(request.method, endpoint, str(response.status_code)).inc()
        REQUEST_LATENCY.labels(request.method, endpoint).observe(duration)
        return response


# ── App lifecycle ────────────────────────────────────────────────────────────
@asynccontextmanager
async def lifespan(app: FastAPI) -> AsyncIterator[None]:
    settings: Settings = app.state.settings
    signals_router.init_dependencies(settings)
    logger.info(
        "signal_service.started",
        environment=settings.environment,
        paper_trading=settings.paper_trading,
    )
    yield
    logger.info("signal_service.stopped")


# ── Application factory ──────────────────────────────────────────────────────
def create_app(settings: Settings | None = None) -> FastAPI:
    settings = settings or Settings()

    app = FastAPI(
        title="Signal Service",
        description=(
            "Generates algorithmic trading signals using configurable strategies "
            "(RSI, Moving Average Cross) on top of Alpaca Markets data.\n\n"
            "**Paper trading only** — no real money at risk during development."
        ),
        version="1.0.0",
        contact={"name": "Trading Bot Team"},
        license_info={"name": "MIT"},
        lifespan=lifespan,
        docs_url="/docs",
        redoc_url="/redoc",
        openapi_url="/openapi.json",
    )

    app.state.settings = settings

    # ── Middleware ───────────────────────────────────────────────────────────
    app.add_middleware(
        CORSMiddleware,
        allow_origins=["*"],
        allow_methods=["*"],
        allow_headers=["*"],
    )
    app.add_middleware(PrometheusMiddleware)

    # ── Prometheus metrics endpoint ──────────────────────────────────────────
    metrics_app = make_asgi_app()
    app.mount("/metrics", metrics_app)

    # ── Health probes ────────────────────────────────────────────────────────
    @app.get(
        "/health/live",
        tags=["Health"],
        summary="Liveness probe",
        response_model=HealthResponse,
    )
    def health_live() -> HealthResponse:
        return HealthResponse(
            status="alive",
            environment=settings.environment,
            paper_trading=settings.paper_trading,
        )

    @app.get(
        "/health/ready",
        tags=["Health"],
        summary="Readiness probe",
        response_model=HealthResponse,
    )
    def health_ready() -> HealthResponse:
        return HealthResponse(
            status="ready",
            environment=settings.environment,
            paper_trading=settings.paper_trading,
        )

    # ── API routes ───────────────────────────────────────────────────────────
    app.include_router(signals_router.router)

    return app


app = create_app()

if __name__ == "__main__":
    _settings = Settings()
    uvicorn.run(
        "src.main:app",
        host="0.0.0.0",
        port=_settings.port,
        reload=not _settings.is_production,
        log_level=_settings.log_level.lower(),
    )
