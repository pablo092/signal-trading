"""Signal Service — FastAPI application entrypoint.

Swagger UI:  http://localhost:8000/docs
ReDoc:       http://localhost:8000/redoc
OpenAPI JSON: http://localhost:8000/openapi.json
Metrics:     http://localhost:8000/metrics
"""

from __future__ import annotations

from collections.abc import AsyncIterator
from contextlib import asynccontextmanager

import structlog
import uvicorn
from fastapi import FastAPI
from fastapi.middleware.cors import CORSMiddleware
from prometheus_client import make_asgi_app

from src.api.errors import install as install_error_handling
from src.api.routes import signals as signals_router
from src.api.schemas import HealthResponse
from src.infrastructure.auth import TokenStore
from src.infrastructure.config import Settings
from src.infrastructure.observability import PrometheusMiddleware, init_tracing

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

# Prometheus metrics and tracing live in src.infrastructure.observability (BL-17).


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
    app.state.shutdown_tracing()  # flush buffered spans before exit
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
    # Fail fast on an invalid token table (the error names the entry id, never its content).
    app.state.tokens = TokenStore.load(settings.auth_tokens, settings.auth_tokens_file)
    if len(app.state.tokens) == 0:
        logger.warning("auth.no_tokens_configured", effect="every /api/v1 request returns 401")

    # ── Middleware ───────────────────────────────────────────────────────────
    app.add_middleware(
        CORSMiddleware,
        allow_origins=["*"],
        allow_methods=["*"],
        allow_headers=["*"],
        expose_headers=["X-Request-ID"],
    )
    app.add_middleware(PrometheusMiddleware)
    # Added last = outermost: every response and log line carries the request ID.
    install_error_handling(app)

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

    # ── Tracing: OTLP export only when OTEL_EXPORTER_OTLP_ENDPOINT is set ─────
    app.state.shutdown_tracing = init_tracing(
        app, service_version=app.version, environment=settings.environment
    )

    return app


# No module-level app: settings (and the token table) are loaded when the server starts,
# so importing this module never requires secrets. Run with:
#   uvicorn --factory src.main:create_app
if __name__ == "__main__":
    _settings = Settings()
    uvicorn.run(
        "src.main:create_app",
        factory=True,
        host="0.0.0.0",
        port=_settings.port,
        reload=not _settings.is_production,
        log_level=_settings.log_level.lower(),
    )
