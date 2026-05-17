// Bot Engine — main entrypoint.
// Wires the dependency injection container manually (no DI framework needed).
// Clean Architecture: dependencies flow inward (infra → app → domain).
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"

	"github.com/trading-bot/bot-engine/internal/application/eventbus"
	"github.com/trading-bot/bot-engine/internal/application/usecases"
	"github.com/trading-bot/bot-engine/internal/domain/entities"
	"github.com/trading-bot/bot-engine/internal/infrastructure/broker"
	"github.com/trading-bot/bot-engine/internal/infrastructure/config"
	httphandlers "github.com/trading-bot/bot-engine/internal/infrastructure/http/handlers"
	"github.com/trading-bot/bot-engine/internal/infrastructure/messaging"
	"github.com/trading-bot/bot-engine/internal/infrastructure/middleware"
	"github.com/trading-bot/bot-engine/internal/infrastructure/repository"
	"github.com/trading-bot/bot-engine/internal/infrastructure/signalclient"
)

func main() {
	// ── Config ──────────────────────────────────────────────
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	// ── Logger ──────────────────────────────────────────────
	var logger *zap.Logger
	if cfg.Environment == "production" {
		logger, _ = zap.NewProduction()
	} else {
		logger, _ = zap.NewDevelopment()
	}
	defer logger.Sync() //nolint:errcheck

	logger.Info("bot_engine.starting",
		zap.String("environment", cfg.Environment),
		zap.Bool("paper_trading", cfg.PaperTrading),
		zap.Int("port", cfg.Port),
	)

	// ── Infrastructure layer ─────────────────────────────────
	alpacaBroker := broker.NewAlpacaBroker(cfg, logger)
	signalSvc    := signalclient.New(cfg.SignalServiceURL, logger)
	bus          := eventbus.New(logger)
	hub          := messaging.NewWSHub(logger)

	// Wire events to WebSocket broadcasts
	bus.Subscribe("OrderPlaced", hub.HandleDomainEvent)
	bus.Subscribe("OrderFilled", hub.HandleDomainEvent)

	// ── Application layer ────────────────────────────────────
	// In-memory order repo (swap for PostgreSQL repo in production)
	orderRepo    := repository.NewInMemoryOrderRepo()
	riskRules    := entities.DefaultRiskRules()
	placeOrderUC := usecases.NewPlaceOrderUseCase(
		alpacaBroker, orderRepo, signalSvc, bus, riskRules, logger,
	)

	// ── Prometheus registry ──────────────────────────────────
	reg := prometheus.NewRegistry()

	// ── HTTP server (Gin) ─────────────────────────────────────
	if cfg.Environment == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	router := gin.New()
	router.Use(
		middleware.Recovery(logger),
		middleware.RequestID(),
		middleware.StructuredLogger(logger),
		middleware.Tracing("bot-engine"),
		middleware.PrometheusMetrics(reg),
	)

	// Health probes
	router.GET("/health/live", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "alive"})
	})
	router.GET("/health/ready", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":      "ready",
			"service":     "bot-engine",
			"ws_clients":  hub.ConnectedClients(),
			"paper_mode":  cfg.PaperTrading,
		})
	})

	// Metrics
	router.GET("/metrics", gin.WrapH(promhttp.HandlerFor(reg, promhttp.HandlerOpts{})))

	// WebSocket (Flutter app)
	router.GET("/ws", func(c *gin.Context) {
		clientID := c.Query("client_id")
		if clientID == "" {
			clientID = "anon"
		}
		hub.ServeWS(c.Writer, c.Request, clientID)
	})

	// API v1
	v1 := router.Group("/api/v1")
	httphandlers.NewOrdersHandler(placeOrderUC, logger).RegisterRoutes(v1)
	httphandlers.NewPortfolioHandler(alpacaBroker, logger).RegisterRoutes(v1)

	// ── Graceful shutdown ─────────────────────────────────────
	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go hub.Run(ctx)

	go func() {
		logger.Info("bot_engine.http_listening", zap.Int("port", cfg.Port))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("http.server_error", zap.Error(err))
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit

	logger.Info("bot_engine.shutting_down", zap.String("signal", sig.String()))
	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("http.shutdown_error", zap.Error(err))
	}

	logger.Info("bot_engine.stopped")
}
