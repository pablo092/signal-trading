# ════════════════════════════════════════════════════════════════
# Trading Bot — Makefile
# Usage: make <target>
# ════════════════════════════════════════════════════════════════

.DEFAULT_GOAL := help
.PHONY: help dev stop build ci test lint audit clean deploy-fly

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

# ── Local CI (BL-01) ─────────────────────────────────────────────
# Same gates as the GitHub workflows, runnable while Actions is unavailable.
ci: lint test audit-python audit-go compose-check ## Run every local quality gate
	@echo "$(CYAN)All local CI gates passed$(RESET)"

compose-check: ## Validate docker-compose.yml (default and full profiles)
	@ALPACA_API_KEY=ci ALPACA_SECRET_KEY=ci docker compose config --quiet
	@ALPACA_API_KEY=ci ALPACA_SECRET_KEY=ci docker compose --profile full config --quiet
	@echo "compose: ok"

# ── Testing ───────────────────────────────────────────────────────
# Python targets use each service's .venv (see signal-service/README.md).
VENV_BIN := $(if $(filter Windows_NT,$(OS)),.venv/Scripts,.venv/bin)
# -race needs cgo + a C toolchain; off by default on Windows. Override: make test GO_RACE=-race
GO_RACE  ?= $(if $(filter Windows_NT,$(OS)),,-race)
STATICCHECK_VERSION := v0.8.2

test: test-signal test-engine test-gateway ## Run all tests

test-signal: ## Run signal-service unit tests
	@echo "$(CYAN)Testing signal-service...$(RESET)"
	cd signal-service && $(VENV_BIN)/python -m pytest tests/ --cov=src --cov-report=term-missing --cov-fail-under=80

test-gateway: ## Run agent-gateway tests
	@echo "$(CYAN)Testing agent-gateway...$(RESET)"
	cd agent-gateway && $(VENV_BIN)/python -m pytest --cov=agent_gateway

test-engine: ## Run bot-engine unit tests
	@echo "$(CYAN)Testing bot-engine...$(RESET)"
	cd bot-engine && go test ./... -count=1 $(GO_RACE)

test-engine-cover: ## bot-engine tests with coverage report
	cd bot-engine && \
		go test ./... -coverprofile=coverage.out && \
		go tool cover -html=coverage.out -o coverage.html && \
		echo "Coverage report: bot-engine/coverage.html"

# ── Linting ───────────────────────────────────────────────────────
lint: lint-signal lint-engine lint-gateway ## Run all linters

lint-signal: ## Lint signal-service (ruff + mypy)
	@echo "$(CYAN)Linting signal-service...$(RESET)"
	cd signal-service && $(VENV_BIN)/python -m ruff check src/ tests/ && $(VENV_BIN)/python -m mypy src/

lint-gateway: ## Lint agent-gateway (ruff + mypy)
	@echo "$(CYAN)Linting agent-gateway...$(RESET)"
	cd agent-gateway && $(VENV_BIN)/python -m ruff check src tests && $(VENV_BIN)/python -m mypy src

lint-engine: ## Lint bot-engine (go vet + staticcheck)
	@echo "$(CYAN)Linting bot-engine...$(RESET)"
	cd bot-engine && go vet ./... && go run honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION) ./...

# ── Dependency scanning (BL-12) ───────────────────────────────────
PIP_AUDIT_VERSION   := 2.10.1
GOVULNCHECK_VERSION := v1.9.0
OSV_SCANNER_VERSION := v2.7.1
# uvx from PATH, else the one installed in signal-service's venv (uv is a dev dependency).
UVX ?= $(or $(shell command -v uvx 2>/dev/null),signal-service/$(VENV_BIN)/uvx)

audit: audit-python audit-go audit-dart ## Scan all dependencies for known vulnerabilities

audit-python: ## pip-audit on the hashed Python lockfiles
	@for f in signal-service/requirements.lock signal-service/requirements-dev.lock 	          agent-gateway/requirements.lock agent-gateway/requirements-dev.lock; do 		echo "$(CYAN)pip-audit $$f$(RESET)"; 		$(UVX) --from pip-audit==$(PIP_AUDIT_VERSION) pip-audit --require-hashes --disable-pip -r $$f || exit 1; 	done

audit-go: ## govulncheck on bot-engine (reachable vulnerabilities only)
	cd bot-engine && go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

audit-dart: ## OSV-Scanner on the Flutter lockfile
	go run github.com/google/osv-scanner/v2/cmd/osv-scanner@$(OSV_SCANNER_VERSION) scan source --lockfile app/pubspec.lock

# ── Database ──────────────────────────────────────────────────────
db-shell: ## Open psql shell
	docker compose exec postgres psql -U postgres -d tradingbot

db-reset: ## Drop and recreate the local database (destroys local data, including the audit log)
	docker compose exec postgres psql -U postgres -c "DROP DATABASE IF EXISTS tradingbot;"
	docker compose exec postgres psql -U postgres -c "CREATE DATABASE tradingbot;"
	docker compose restart bot-engine  # migrations run on start

# ── Cloud deploy (Fly.io) ─────────────────────────────────────────
deploy-fly: deploy-signal-fly deploy-engine-fly ## Deploy both services to Fly.io

deploy-signal-fly: ## Deploy signal-service to Fly.io
	@echo "$(CYAN)Deploying signal-service to Fly.io...$(RESET)"
	cd signal-service && flyctl deploy --remote-only --ha=false

deploy-engine-fly: ## Deploy bot-engine to Fly.io
	@echo "$(CYAN)Deploying bot-engine to Fly.io...$(RESET)"
	cd bot-engine && flyctl deploy --remote-only --ha=false

fly-secrets: ## Set Alpaca secrets on Fly.io — reads from .env
	@test -f .env || (echo ".env not found — copy .env.example first" && exit 1)
	flyctl secrets set \
		ALPACA_API_KEY=$$(grep ALPACA_API_KEY .env | cut -d= -f2) \
		ALPACA_SECRET_KEY=$$(grep ALPACA_SECRET_KEY .env | cut -d= -f2) \
		--app trading-bot-signal-service
	flyctl secrets set \
		ALPACA_API_KEY=$$(grep ALPACA_API_KEY .env | cut -d= -f2) \
		ALPACA_SECRET_KEY=$$(grep ALPACA_SECRET_KEY .env | cut -d= -f2) \
		--app trading-bot-engine

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
