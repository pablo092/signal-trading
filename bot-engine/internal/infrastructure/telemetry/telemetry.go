// Package telemetry configures OpenTelemetry tracing (BL-17, TD-14).
//
// Tracing exports over OTLP/HTTP only when OTEL_EXPORTER_OTLP_ENDPOINT (or
// OTEL_EXPORTER_OTLP_TRACES_ENDPOINT) is set, e.g. http://jaeger:4318 in the
// compose `full` profile. Otherwise spans stay no-ops, but W3C trace context is
// still propagated so a downstream service can join the trace.
package telemetry

import (
	"context"
	"os"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.uber.org/zap"
)

// Enabled reports whether an OTLP endpoint is configured.
func Enabled() bool {
	return os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" || os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT") != ""
}

// Init installs the propagator and, when enabled, an OTLP tracer provider.
// The returned shutdown flushes pending spans; it is safe to call when disabled.
func Init(ctx context.Context, service, version, environment string, logger *zap.Logger) (shutdown func(context.Context) error, err error) {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	if !Enabled() {
		logger.Info("telemetry.tracing_disabled: set OTEL_EXPORTER_OTLP_ENDPOINT to export spans")
		return func(context.Context) error { return nil }, nil
	}
	exporter, err := otlptracehttp.New(ctx) // endpoint, headers and TLS come from the OTEL_* env vars
	if err != nil {
		return nil, err
	}
	res, err := resource.Merge(resource.Default(), resource.NewWithAttributes(semconv.SchemaURL,
		semconv.ServiceName(service), semconv.ServiceVersion(version), semconv.DeploymentEnvironmentNameKey.String(environment)))
	if err != nil {
		return nil, err
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter, sdktrace.WithBatchTimeout(5*time.Second)),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) { logger.Warn("telemetry.export_error", zap.Error(err)) }))
	logger.Info("telemetry.tracing_enabled")
	return tp.Shutdown, nil
}
