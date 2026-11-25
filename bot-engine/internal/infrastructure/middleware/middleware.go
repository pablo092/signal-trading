// Package middleware provides Gin HTTP middleware for the bot engine.
package middleware

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.uber.org/zap"

	"github.com/trading-bot/bot-engine/internal/infrastructure/http/apierror"
	"github.com/trading-bot/bot-engine/internal/infrastructure/reqid"
)

// Recovery recovers from panics, logs them with the request ID and stack, and
// answers with the standard error envelope. Register it after RequestID.
func Recovery(logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if r := recover(); r != nil {
				logger.Error("http.panic_recovered",
					zap.Any("panic", r),
					zap.String("path", c.Request.URL.Path),
					zap.String("request_id", c.GetString(reqid.GinKey)),
					zap.Stack("stack"),
				)
				apierror.Abort(c, http.StatusInternalServerError, apierror.CodeInternal, "internal error")
			}
		}()
		c.Next()
	}
}

// RequestID assigns each request a correlation ID: the caller's X-Request-ID
// when it is log-safe, otherwise a new UUID. The ID is echoed in the response
// header, stored in the gin and request contexts, and forwarded on outbound calls.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := reqid.FromHeader(c.GetHeader(reqid.Header))
		c.Set(reqid.GinKey, id)
		c.Header(reqid.Header, id)
		c.Request = c.Request.WithContext(reqid.WithContext(c.Request.Context(), id))
		c.Next()
	}
}

// StructuredLogger logs each request with duration, status, and request ID.
func StructuredLogger(logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		logger.Info("http.request",
			zap.String("method", c.Request.Method),
			zap.String("path", c.FullPath()),
			zap.Int("status", c.Writer.Status()),
			zap.Duration("latency", time.Since(start)),
			zap.String("request_id", c.GetString(reqid.GinKey)),
			zap.String("client_ip", c.ClientIP()),
		)
	}
}

// Tracing injects OpenTelemetry trace context into each request.
func Tracing(serviceName string) gin.HandlerFunc {
	propagator := otel.GetTextMapPropagator()
	tracer := otel.Tracer(serviceName)

	return func(c *gin.Context) {
		ctx := propagator.Extract(c.Request.Context(), propagation.HeaderCarrier(c.Request.Header))
		ctx, span := tracer.Start(ctx, c.FullPath())
		defer span.End()
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

// PrometheusMetrics tracks HTTP request counts and latency histograms.
func PrometheusMetrics(reg prometheus.Registerer) gin.HandlerFunc {
	requestsTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "Total number of HTTP requests.",
	}, []string{"method", "path", "status"})

	requestDuration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "HTTP request latency.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "path"})

	reg.MustRegister(requestsTotal, requestDuration)

	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		path := c.FullPath()
		if path == "" {
			path = "unknown"
		}

		statusStr := http.StatusText(c.Writer.Status())
		requestsTotal.WithLabelValues(c.Request.Method, path, statusStr).Inc()
		requestDuration.WithLabelValues(c.Request.Method, path).Observe(time.Since(start).Seconds())
	}
}
