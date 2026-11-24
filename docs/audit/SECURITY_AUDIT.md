# Security Audit — M0

**Scope:** defensive review of all tracked files, git history (pattern scan), and local untracked `.env*` files. For the local files, only **key names** were inspected, never values.
**Out of scope:** dynamic testing (the stack was not started), dependency CVE scanning (no scanner available offline; listed as backlog item BL-12).

No secret values appear in this document.

## 1. Summary

| Severity | Count | IDs |
|---|---|---|
| Critical | 3 | SEC-01, SEC-02, SEC-03 |
| High | 4 | SEC-04, SEC-05, SEC-06, SEC-07 |
| Medium | 5 | SEC-08 … SEC-12 |
| Low | 3 | SEC-13 … SEC-15 |

The dominant risk is **trading safety**, not data confidentiality. An unauthenticated caller can place broker orders. Paper mode depends on a single unchecked URL. Nothing prevents duplicate or runaway execution.

## 2. Credentials and secrets

| Check | Result | Evidence |
|---|---|---|
| Hardcoded Alpaca keys in tracked files | **None found** | K8s manifests contain placeholders only: `REPLACE_WITH_REAL_KEY` (base64-encoded in `bot-engine/bot-engine.yml:37-38`, plain in `signal-service/signal-service.yml:40-41`). CI uses obvious dummies (`PKTEST000…`). |
| Secrets in git history | **None found** | Scanned `git log -p --all` for Alpaca key ID patterns (`PK…`, `AK…`), private-key headers, and GitHub tokens: 0 matches. 3 `ALPACA_SECRET_KEY=<value>` matches: 2 are CI dummies, and 1 is the K8s manifest placeholder (blob identical to HEAD). No `.env` file has ever been committed. |
| `.env` handling | OK, with caveat | `.env` is git-ignored (`.gitignore:2`). Caveat: `.env.*` also ignores `.env.example` (SEC-14). |
| Local `.env` mode | Paper | `ALPACA_BASE_URL` host is `paper-api.alpaca.markets`; `PAPER_TRADING=true` (values otherwise not read) |
| Secrets in logs | OK | No key/secret is logged. `bot-engine/main.go:49-53` logs env, paper flag, and port only. |
| Secrets via Makefile | Low risk | `Makefile:101-110` (`fly-secrets`) passes keys as CLI arguments, so they are visible in the process list and shell history (SEC-15). |
| Default credentials | **Present** | Compose fallbacks `postgres/postgres`, `rabbit/rabbit`, `admin/admin` (`docker-compose.yml:111,171-172,211-212`), echoed by `Makefile:24,26`. The K8s ConfigMap embeds a DB URL with `postgres:postgres` (`signal-service/signal-service.yml:27`). → SEC-08 |

**No compromised secret was identified; no rotation is required on the basis of this audit.**

## 3. Findings

### SEC-01 — Critical — Unauthenticated order execution (→ TD-01)
`POST /api/v1/orders/place` triggers broker order submission with no authentication (`bot-engine/main.go:116-118`). It is published on host port 8080 (`docker-compose.yml:78-79`) and on public 80/443 in `bot-engine/fly.toml`. Any local process, LAN host (depending on firewall), or future agent with network access can trade.
**Action:** auth middleware with scoped credentials; execution disabled by default; loopback-only binding in dev.

### SEC-02 — Critical — Live trading not guarded (→ TD-02)
The broker endpoint is whatever `ALPACA_BASE_URL` says (`config.go:33`, `alpaca.go:24-28`). `PAPER_TRADING` is not enforced, and the API always reports `paper_mode: true` (`portfolio.go:57`), which misleads operators.
**Action:** fail-closed startup check binding `PAPER_TRADING=true` to the paper host; separate explicit live acknowledgment; truthful mode reporting.

### SEC-03 — Critical — No idempotency or replay protection (→ TD-03)
Identical requests produce independent orders (`place_order.go:143`; no `client_order_id`, `alpaca.go:36-42`).
**Action:** mandatory idempotency key persisted with a unique constraint and forwarded as `client_order_id`.

### SEC-04 — High — Risk validation incomplete; fail-open path (→ TD-04)
The daily-loss check is a stub returning 0 (`place_order.go:185-188`). The code explicitly continues on PnL errors ("Non-fatal: continue", `place_order.go:113-115`). There is no position-count, market-hours, or short-sale guard.
**Action:** fail-closed risk engine (ADR-005).

### SEC-05 — High — No audit trail and no emergency stop (→ TD-05)
In-memory orders only (`main.go:66-67`); no record of requester identity; no kill switch.
**Action:** append-only audit table; persisted kill switch checked pre-submission; cancel-all routine.

### SEC-06 — High — WebSocket unauthenticated, any origin (→ TD-11)
`CheckOrigin` always returns `true` (`ws_hub.go:21`); no auth on upgrade (`main.go:107-113`). A malicious web page visited by the operator can open a WS to `localhost:8080` and read order events (cross-site WebSocket hijacking). A concurrent-write race (`ws_hub.go:120-128` vs `154-163`) also enables crash-based DoS.
**Action:** origin allowlist, token on upgrade, single-writer pump.

### SEC-07 — High — Internal services exposed on all interfaces (→ TD-16)
Postgres, TimescaleDB, Redis (no password), Prometheus (with `--web.enable-lifecycle`: unauthenticated `POST /-/quit`), Loki, and the Jaeger collectors are published on `0.0.0.0` (`docker-compose.yml:116-117,137-138,155-156,196-201,236-237,263-268`).
**Action:** remove host ports or bind to `127.0.0.1`; set the Redis password; drop the lifecycle flag.

### SEC-08 — Medium — Default credentials with silent fallback
`${POSTGRES_PASSWORD:-postgres}` and similar defaults mean a missing `.env` still starts the stack with known passwords (see §2).
**Action:** use `${VAR:?required}` syntax; no fallbacks for passwords.

### SEC-09 — Medium — signal-service unauthenticated, CORS `*`, no rate limit (→ TD-20)
`signal-service/src/main.py:105-110`. Each call consumes Alpaca data quota (`alpaca_market_data.py:69`), so it can be abused to exhaust quota or DoS the service.
**Action:** service auth, rate limiting, caching.

### SEC-10 — Medium — Error detail leakage (→ TD-19)
Raw broker and internal errors are returned to callers (`orders.go:75`, `portfolio.go:49,84`, `signals.py:121-144`).

### SEC-11 — Medium — Request validation gaps on the order path
`placeOrderRequest` (`orders.go:32-37`) validates presence only. `Symbol` is not format-checked in Go and is passed straight to signal-service and Alpaca. `Strategy` is not allowlisted. No max notional or max quantity cap exists independent of equity-based sizing (`risk_rules.go:51-57`).
**Action:** symbol regex, strategy allowlist, absolute notional caps.

### SEC-12 — Medium — Deployment manifests unsafe as written (→ TD-17)
- The engine HPA allows up to 3 replicas with in-memory state, so duplicate execution is possible.
- Credentials sit in a ConfigMap.
- There is no NetworkPolicy.
- Deploy jobs in CI target `environment: production` with placeholder images.
- Promtail mounts `docker.sock` read-only (`docker-compose.yml:248`), which is still effectively host-level access to Docker metadata.

### SEC-13 — Low — Supply-chain hygiene
- Python deps use `>=` ranges with no lock file (`pyproject.toml`, `requirements.txt`).
- The Flutter `pubspec.lock` is git-ignored.
- `timescaledb:latest-pg16` is unpinned.
- `MishaKav/pytest-coverage-comment@main` is pinned to a branch (`.github/workflows/signal-service.yml:94`).
- Dev tooling ships in the runtime image (`Dockerfile:14`).
- Go deps are pinned via `go.sum` (OK).

### SEC-14 — Low — `.env.example` git-ignored
`.gitignore:3` matches `.env.example`, so contributors may create `.env` from memory or copy real values around. Add `!.env.example`.

### SEC-15 — Low — Secrets passed as CLI args
`Makefile:103-110` exposes keys in process arguments and shell history. Prefer `flyctl secrets import < file`.

## 4. Container and runtime posture

| Item | Status | Evidence |
|---|---|---|
| Non-root containers | OK | `bot-engine/Dockerfile:20,24` (`appuser`); `signal-service/Dockerfile:20,29` |
| Minimal base images | OK | `alpine:3.20`, `python:3.11-slim` |
| K8s securityContext | Engine OK, signal-service partial | `bot-engine.yml:104-110` drops all capabilities and uses a read-only root filesystem. `signal-service.yml:74-77` sets runAsNonRoot only. |
| Privileged containers | None | |
| Static Go binary | OK | `CGO_ENABLED=0`, `-static` |

## 5. Trading-safety checklist

| Control | Present? | Evidence |
|---|---|---|
| Paper mode enforced | **No** | SEC-02 |
| Authenticated order requests | **No** | SEC-01 |
| Authorization / scopes | **No** | — |
| Idempotency | **No** | SEC-03 |
| Pre-trade risk validation (complete, fail-closed) | **Partial** | Confidence and size only; SEC-04 |
| Max notional / quantity caps | **No** | SEC-11 |
| Market-hours check | **No** | `IsMarketOpen` unused in order path |
| Audit log | **No** | SEC-05 |
| Emergency stop | **No** | SEC-05 |
| Human approval workflow | **No** | — |
| Single-writer execution guarantee | **No** | HPA up to 3 (SEC-12) |

## 6. Implications for agent integration (MCP)

Exposing the current bot-engine API to an agent would give the agent **unrestricted, unaudited, non-idempotent order execution against an account whose live/paper status is unverified**. Therefore:

1. M1 agent tools must be **read-only** and must not have network reachability to `/api/v1/orders/*`.
2. Execution-capable tools are gated on ADR-003 and ADR-005 being implemented, not merely accepted.

See [ADR-003](../adr/ADR-003-AGENT-SECURITY.md) and [ADR-005](../adr/ADR-005-TRADING-EXECUTION-SAFETY.md).
