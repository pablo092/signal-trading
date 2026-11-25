package telemetry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"go.opentelemetry.io/otel"
	"go.uber.org/zap"
)

func TestDisabledWithoutEndpoint(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	shutdown, err := Init(context.Background(), "bot-engine", "test", "test", zap.NewNop())
	if err != nil || shutdown(context.Background()) != nil {
		t.Fatalf("init: %v", err)
	}
	if Enabled() {
		t.Fatal("should be disabled")
	}
}

func TestExportsSpansToTheOTLPEndpoint(t *testing.T) {
	var hits atomic.Int32
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/traces" {
			hits.Add(1)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer collector.Close()
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", collector.URL)

	shutdown, err := Init(context.Background(), "bot-engine", "test", "test", zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	_, span := otel.Tracer("test").Start(context.Background(), "unit")
	span.End()
	if err := shutdown(context.Background()); err != nil { // flushes the batch
		t.Fatal(err)
	}
	if hits.Load() == 0 {
		t.Fatal("no span reached the collector")
	}
}
