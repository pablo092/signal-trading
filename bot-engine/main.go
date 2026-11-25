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
	"go.uber.org/zap"

	"github.com/trading-bot/bot-engine/internal/application/eventbus"
	"github.com/trading-bot/bot-engine/internal/application/usecases"
	"github.com/trading-bot/bot-engine/internal/domain/entities"
	"github.com/trading-bot/bot-engine/internal/domain/ports"
	"github.com/trading-bot/bot-engine/internal/infrastructure/auditlog"
	"github.com/trading-bot/bot-engine/internal/infrastructure/auth"
	"github.com/trading-bot/bot-engine/internal/infrastructure/broker"
	"github.com/trading-bot/bot-engine/internal/infrastructure/config"
	"github.com/trading-bot/bot-engine/internal/infrastructure/http/server"
	"github.com/trading-bot/bot-engine/internal/infrastructure/messaging"
	"github.com/trading-bot/bot-engine/internal/infrastructure/postgres"
	"github.com/trading-bot/bot-engine/internal/infrastructure/repository"
	"github.com/trading-bot/bot-engine/internal/infrastructure/signalclient"
	"github.com/trading-bot/bot-engine/internal/infrastructure/telemetry"
)

// version is stamped at build time (-ldflags "-X main.version=...") and
// reported as the OTel service.version.
var version = "dev"

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
		zap.String("broker_host", cfg.BrokerHost()),
		zap.Bool("order_execution_enabled", cfg.OrderExecutionEnabled),
		zap.Int("port", cfg.Port),
	)

	// ── Tracing (BL-17): OTLP export only when an endpoint is configured ──
	shutdownTracing, err := telemetry.Init(context.Background(), "bot-engine", version, cfg.Environment, logger)
	if err != nil {
		// Observability must never stop trading safety paths from starting.
		logger.Error("telemetry.init_failed", zap.Error(err))
	}

	// ── Infrastructure layer ─────────────────────────────────
	// ADR-004: the adapter verifies paper mode against Alpaca itself and refuses
	// to start otherwise (fail closed).
	verifyCtx, verifyCancel := context.WithTimeout(context.Background(), 2*broker.RequestTimeout)
	alpacaBroker, err := broker.NewAlpacaBroker(verifyCtx, cfg, logger)
	verifyCancel()
	if err != nil {
		logger.Fatal("broker.init_failed", zap.Error(err))
	}
	signalSvc := signalclient.New(cfg.SignalServiceURL, cfg.SignalServiceToken, logger)
	bus := eventbus.New(logger)
	hub := messaging.NewWSHub(cfg.WSAllowedOrigins, logger)

	// Wire events to WebSocket broadcasts
	// Relay domain events to WebSocket clients as contract events (BL-16).
	bus.Subscribe(usecases.EventSignalReceived, hub.HandleDomainEvent)
	bus.Subscribe(usecases.EventOrderPlaced, hub.HandleDomainEvent)

	// ── Persistence (BL-14): Postgres when DATABASE_URL is set, else in memory ──
	var (
		orderRepo  ports.OrderRepositoryPort
		auditLog   ports.AuditLogPort
		killStore  ports.KillSwitchStore
		closeStore = func() {}
	)
	if cfg.DatabaseURL != "" {
		pool, err := postgres.Open(context.Background(), cfg.DatabaseURL)
		if err != nil {
			logger.Fatal("postgres.open_failed", zap.Error(err))
		}
		applied, err := postgres.Migrate(context.Background(), pool)
		if err != nil {
			logger.Fatal("postgres.migrate_failed", zap.Error(err))
		}
		logger.Info("postgres.ready", zap.Strings("migrations_applied", applied))
		orderRepo, auditLog, killStore = postgres.NewOrderRepo(pool), postgres.NewAuditLog(pool), postgres.NewKillSwitchStore(pool)
		closeStore = pool.Close
	} else {
		logger.Warn("persistence.in_memory: DATABASE_URL is not set; orders and the kill-switch flag do not survive a restart, audit events go to the log only")
		orderRepo, auditLog, killStore = repository.NewInMemoryOrderRepo(), auditlog.NewMemory(logger), &auditlog.MemoryKillSwitch{}
	}
	defer closeStore()

	// ── Kill switch (BL-19): loaded before any traffic; KILL_SWITCH=true forces it on ──
	killSwitch := usecases.NewKillSwitchService(killStore, alpacaBroker, auditLog, bus, cfg.KillSwitchForced, logger)
	if err := killSwitch.Init(context.Background()); err != nil {
		logger.Error("kill_switch.init_failed: trading is refused until the state can be read", zap.Error(err))
	}
	bus.Subscribe(usecases.EventKillSwitchChanged, hub.HandleDomainEvent)

	// ── Application layer ────────────────────────────────────
	riskRules := entities.DefaultRiskRules()
	if err := riskRules.Validate(); err != nil {
		logger.Fatal("risk_rules.invalid", zap.Error(err))
	}
	placeOrderUC := usecases.NewPlaceOrderUseCase(alpacaBroker, orderRepo, signalSvc, bus, riskRules, logger).
		WithAudit(auditLog).WithKillSwitch(killSwitch)

	// ── Prometheus registry ──────────────────────────────────
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "bot_engine_ws_clients_connected",
			Help: "WebSocket clients currently connected to the hub.",
		}, func() float64 { return float64(hub.ConnectedClients()) }),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "bot_engine_kill_switch_engaged",
			Help: "1 while the kill switch blocks new orders (stored state or KILL_SWITCH env), else 0.",
		}, func() float64 {
			if killSwitch.EngagedNow() {
				return 1
			}
			return 0
		}),
	)

	// ── Auth (ADR-003): scoped service tokens, deny by default ─
	tokens, err := auth.LoadStore(cfg.AuthTokensJSON, cfg.AuthTokensFile)
	if err != nil {
		logger.Fatal("auth.token_table_invalid", zap.Error(err))
	}
	if tokens.Len() == 0 {
		logger.Warn("auth.no_tokens_configured: every /api/v1 and /ws request will be rejected with 401")
	}

	// ── HTTP server (Gin) ─────────────────────────────────────
	if cfg.Environment == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	router := server.NewRouter(server.Deps{
		Config:     cfg,
		Tokens:     tokens,
		PlaceOrder: placeOrderUC,
		KillSwitch: killSwitch,
		Broker:     alpacaBroker,
		Hub:        hub,
		Registry:   reg,
		Logger:     logger,
	})

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
	if shutdownTracing != nil {
		if err := shutdownTracing(shutdownCtx); err != nil {
			logger.Warn("telemetry.shutdown_error", zap.Error(err)) // flush of buffered spans failed
		}
	}

	logger.Info("bot_engine.stopped")
}
