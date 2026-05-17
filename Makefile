# ════════════════════════════════════════════════════════════════
# Trading Bot — Makefile
# Usage: make <target>
# ════════════════════════════════════════════════════════════════

.DEFAULT_GOAL := help
.PHONY: help dev stop build test lint clean deploy-fly

# ── Colors ───────────────────────────────────────────────────────
CYAN  := \033[0;36m
RESET := \033[0m

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "$(CYAN)%-20s$(RESET) %s\n", $$1, $$2}'

# ── Local dev ─────────────────────────────────────────────────────
dev: ## Start full stack (build + up -d)
	@echo "$(CYAN)Starting trading bot stack...$(RESET)"
	docker compose up -d --build
	@echo ""
	@echo "  signal-service  → http://localhost:8000/docs"
	@echo "  bot-engine      → http://localhost:8080/health/ready"
	@echo "  Grafana         → http://localhost:3000  (admin/admin)"
	@echo "  Jaeger          → http://localhost:16686"
	@echo "  RabbitMQ UI     → http://localhost:15672 (rabbit/rabbit)"
	@echo "  Prometheus      → http://localhost:9090"

stop: ## Stop all containers
	docker compose down

restart: ## Restart a specific service — usage: make restart s=signal-service
	docker compose restart $(s)

logs: ## Tail logs for a service — usage: make logs s=signal-service
	docker compose logs -f $(s)

ps: ## Show running containers
	docker compose ps

# ── Build ─────────────────────────────────────────────────────────
build: ## Build all Docker images
	docker compose build --no-cache

build-signal: ## Build signal-service image only
	docker compose build --no-cache signal-service

build-engine: ## Build bot-engine image only
	docker compose build --no-cache bot-engine

# ── Testing ───────────────────────────────────────────────────────
test: test-signal test-engine ## Run all tests

test-signal: ## Run signal-service unit tests
	@echo "$(CYAN)Testing signal-service...$(RESET)"
	cd signal-service && \
		pip install -q -e ".[dev]" && \
		pytest tests/ -v --cov=src --cov-report=term-missing --cov-fail-under=80

test-engine: ## Run bot-engine unit tests
	@echo "$(CYAN)Testing bot-engine...$(RESET)"
	cd bot-engine && go test ./... -v -race -count=1

test-engine-cover: ## bot-engine tests with coverage report
	cd bot-engine && \
		go test ./... -coverprofile=coverage.out && \
		go tool cover -html=coverage.out -o coverage.html && \
		echo "Coverage report: bot-engine/coverage.html"

# ── Linting ───────────────────────────────────────────────────────
lint: lint-signal lint-engine ## Run all linters

lint-signal: ## Lint signal-service (ruff + mypy)
	@echo "$(CYAN)Linting signal-service...$(RESET)"
	cd signal-service && ruff check src/ tests/ && ruff format --check src/ tests/ && mypy src/

lint-engine: ## Lint bot-engine (go vet + staticcheck)
	@echo "$(CYAN)Linting bot-engine...$(RESET)"
	cd bot-engine && go vet ./... && staticcheck ./...

# ── Database ──────────────────────────────────────────────────────
db-shell: ## Open psql shell
	docker compose exec postgres psql -U postgres -d tradingbot

db-reset: ## Drop and recreate the database
	docker compose exec postgres psql -U postgres -c "DROP DATABASE IF EXISTS tradingbot;"
	docker compose exec postgres psql -U postgres -c "CREATE DATABASE tradingbot;"
	docker compose exec postgres psql -U postgres -d tradingbot -f /docker-entrypoint-initdb.d/init.sql

# ── Cloud deploy (Fly.io) ─────────────────────────────────────────
deploy-fly: deploy-signal-fly deploy-engine-fly ## Deploy both services to Fly.io

deploy-signal-fly: ## Deploy signal-service to Fly.io
	@echo "$(CYAN)Deploying signal-service to Fly.io...$(RESET)"
	cd signal-service && flyctl deploy --remote-only

deploy-engine-fly: ## Deploy bot-engine to Fly.io
	@echo "$(CYAN)Deploying bot-engine to Fly.io...$(RESET)"
	cd bot-engine && flyctl deploy --remote-only

fly-secrets: ## Set Alpaca secrets on Fly.io — reads from .env
	@test -f .env || (echo ".env not found — copy .env.example first" && exit 1)
	flyctl secrets set \
		ALPACA_API_KEY=$$(grep ALPACA_API_KEY .env | cut -d= -f2) \
		ALPACA_SECRET_KEY=$$(grep ALPACA_SECRET_KEY .env | cut -d= -f2) \
		--app signal-service
	flyctl secrets set \
		ALPACA_API_KEY=$$(grep ALPACA_API_KEY .env | cut -d= -f2) \
		ALPACA_SECRET_KEY=$$(grep ALPACA_SECRET_KEY .env | cut -d= -f2) \
		--app bot-engine

# ── Kubernetes ────────────────────────────────────────────────────
k8s-apply: ## Apply all Kubernetes manifests
	kubectl apply -f signal-service/signal-service.yml
	kubectl apply -f bot-engine/bot-engine.yml

k8s-status: ## Show K8s pod status
	kubectl get pods -n tradingbot -o wide

k8s-logs: ## Tail K8s logs — usage: make k8s-logs s=signal-service
	kubectl logs -n tradingbot -l app=$(s) -f --tail=100

# ── Cleanup ───────────────────────────────────────────────────────
clean: ## Remove containers, volumes, and build cache
	docker compose down -v --remove-orphans
	docker system prune -f
