"""Metrics and tracing (BL-17).

Metrics live here, not in ``src.main``, so routes can record them without a
circular import. Tracing exports over OTLP/HTTP only when an OTLP endpoint is
configured through the standard ``OTEL_EXPORTER_OTLP_*`` variables; otherwise
spans are never created and nothing leaves the process.
"""

from __future__ import annotations

import os
import time
from collections.abc import Callable

import structlog
from fastapi import FastAPI
from prometheus_client import Counter, Histogram
from starlette.middleware.base import BaseHTTPMiddleware, RequestResponseEndpoint
from starlette.requests import Request
from starlette.responses import Response

logger = structlog.get_logger(__name__)

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

UNMATCHED_ROUTE = "unmatched"
_ENDPOINT_VARS = ("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "OTEL_EXPORTER_OTLP_ENDPOINT")
# Probes and scrapes would drown the useful spans.
_EXCLUDED_URLS = "health/live,health/ready,metrics"


def route_label(request: Request) -> str:
    """The matched route template (``/api/v1/signals/generate``), never the raw path.

    Raw paths come from the client, so a scanner hitting random URLs would create
    unbounded label values; unmatched requests all share one label.
    """
    route = request.scope.get("route")
    path = getattr(route, "path", None)
    return path if isinstance(path, str) and path else UNMATCHED_ROUTE


class PrometheusMiddleware(BaseHTTPMiddleware):
    async def dispatch(self, request: Request, call_next: RequestResponseEndpoint) -> Response:
        start = time.perf_counter()
        response = await call_next(request)
        duration = time.perf_counter() - start
        endpoint = route_label(request)
        REQUEST_COUNT.labels(request.method, endpoint, str(response.status_code)).inc()
        REQUEST_LATENCY.labels(request.method, endpoint).observe(duration)
        return response


def tracing_enabled() -> bool:
    return any(os.environ.get(v, "").strip() for v in _ENDPOINT_VARS)


def init_tracing(app: FastAPI, *, service_version: str, environment: str) -> Callable[[], None]:
    """Instrument ``app`` and return a shutdown hook that flushes buffered spans.

    W3C trace context from the caller (bot-engine) is honoured either way: the
    default OTel propagator is tracecontext + baggage.
    """
    if not tracing_enabled():
        logger.info(
            "telemetry.tracing_disabled", hint="set OTEL_EXPORTER_OTLP_ENDPOINT to export spans"
        )
        return lambda: None

    from opentelemetry.exporter.otlp.proto.http.trace_exporter import OTLPSpanExporter
    from opentelemetry.instrumentation.fastapi import FastAPIInstrumentor
    from opentelemetry.sdk.resources import Resource
    from opentelemetry.sdk.trace import TracerProvider
    from opentelemetry.sdk.trace.export import BatchSpanProcessor

    provider = TracerProvider(
        resource=Resource.create(
            {
                "service.name": "signal-service",
                "service.version": service_version,
                "deployment.environment.name": environment,
            }
        )
    )
    # The exporter reads endpoint, headers and timeout from OTEL_EXPORTER_OTLP_*.
    provider.add_span_processor(BatchSpanProcessor(OTLPSpanExporter()))
    FastAPIInstrumentor.instrument_app(app, tracer_provider=provider, excluded_urls=_EXCLUDED_URLS)
    # Never log the endpoint: it may embed credentials.
    logger.info("telemetry.tracing_enabled", exporter="otlp_http")

    def shutdown() -> None:
        provider.shutdown()

    return shutdown
