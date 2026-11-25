"""Metrics labels, the signals counter and opt-in OTLP tracing (BL-17)."""

from __future__ import annotations

import pytest
from fastapi.testclient import TestClient
from opentelemetry.sdk.trace.export.in_memory_span_exporter import InMemorySpanExporter
from prometheus_client import REGISTRY
from src.api.routes import signals as signals_router
from src.infrastructure import observability
from src.main import create_app

from tests.test_api import GEN, FakeMarketData, auth, make_settings

TRACE_ID = "4bf92f3577b34da6a3ce929d0e0e4736"


def sample(name: str, **labels: str) -> float:
    return REGISTRY.get_sample_value(name, labels) or 0.0


def make_client(fake: FakeMarketData) -> TestClient:
    app = create_app(make_settings())
    app.dependency_overrides[signals_router.get_market_data] = lambda: fake
    return TestClient(app)


def test_request_metrics_use_route_template_not_raw_path() -> None:
    with make_client(FakeMarketData()) as c:
        before = sample("http_requests_total", method="GET", endpoint="unmatched", status="404")
        c.get("/scanner/probe-1")
        c.get("/scanner/probe-2")
        after = sample("http_requests_total", method="GET", endpoint="unmatched", status="404")
        live = sample("http_requests_total", method="GET", endpoint="/health/live", status="200")
        c.get("/health/live")

    assert after - before == 2
    assert sample("http_requests_total", method="GET", endpoint="/health/live", status="200") == live + 1
    leaked = [
        s
        for m in REGISTRY.collect()
        if m.name == "http_requests"
        for s in m.samples
        if "probe" in s.labels.get("endpoint", "")
    ]
    assert leaked == [], "raw client paths must never become label values"


def test_generated_signal_is_counted() -> None:
    fake = FakeMarketData()
    with make_client(fake) as c:
        r = c.post(GEN, json={"symbol": "AAPL", "strategy": "rsi"}, headers=auth())
        assert r.status_code == 200, r.text
        body = r.json()
        labels = {"direction": body["direction"], "strategy": body["strategy_name"], "symbol": "AAPL"}
        before = sample("signals_generated_total", **labels)
        c.post(GEN, json={"symbol": "AAPL", "strategy": "rsi"}, headers=auth())

    assert sample("signals_generated_total", **labels) == before + 1


def test_tracing_is_off_without_endpoint(monkeypatch: pytest.MonkeyPatch) -> None:
    for var in ("OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"):
        monkeypatch.delenv(var, raising=False)
    assert observability.tracing_enabled() is False


def test_tracing_exports_spans_joined_to_the_callers_trace(monkeypatch: pytest.MonkeyPatch) -> None:
    exporter = InMemorySpanExporter()
    monkeypatch.setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://collector.invalid:4318")
    monkeypatch.setattr(
        "opentelemetry.exporter.otlp.proto.http.trace_exporter.OTLPSpanExporter",
        lambda *a, **k: exporter,
    )

    with make_client(FakeMarketData()) as c:
        r = c.post(
            GEN,
            json={"symbol": "AAPL", "strategy": "rsi"},
            headers={**auth(), "traceparent": f"00-{TRACE_ID}-00f067aa0ba902b7-01"},
        )
        assert r.status_code == 200, r.text
        c.get("/health/live")  # excluded: probes would drown real spans
    # Leaving the client runs the lifespan shutdown, which flushes the batch processor.

    spans = exporter.get_finished_spans()
    server = [s for s in spans if s.kind.name == "SERVER"]
    assert server, f"no server span exported: {[s.name for s in spans]}"
    assert all(format(s.context.trace_id, "032x") == TRACE_ID for s in server)
    assert all(s.resource.attributes["service.name"] == "signal-service" for s in spans)
    assert not any("health" in s.name for s in spans)
