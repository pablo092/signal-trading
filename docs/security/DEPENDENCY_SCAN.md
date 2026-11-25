# Dependency scan — triage (BL-12)

Run with `make audit` (or the `Dependency scan` workflow once GitHub Actions is available).
Scanned 2026-11-25.

| Ecosystem | Tool | Scope | Result |
|---|---|---|---|
| Python (signal-service, agent-gateway) | pip-audit 2.10.1 | runtime + dev lockfiles | No known vulnerabilities |
| Go (bot-engine) | govulncheck v1.9.0 | reachable code only | **9 reachable** (below); 13 more in imported packages and 23 in required modules are not called |
| Dart (app) | OSV-Scanner v2.7.1 | `app/pubspec.lock` | Not run: the local disk was full while building the scanner. Pending |

## bot-engine findings

All reachable findings are in two indirect dependencies pulled in by Gin and the Alpaca SDK:

| Module | Current | Fixed in | Advisories | Reached through |
|---|---|---|---|---|
| `golang.org/x/net` | v0.25.0 | v0.60.0 | GO-2026-6617, GO-2026-6612, GO-2026-6611, GO-2026-6610, GO-2026-6603, GO-2026-5026, GO-2026-4918, GO-2025-3595 | HTTP/2 client (`signalclient.GetSignal`), `html.Tokenizer` (`PlaceOrder` JSON binding) |
| `golang.org/x/text` | v0.15.0 | v0.39.0 | GO-2026-5970 | Unicode normalization in the HTTP/2 client |

**Severity in context:** bot-engine is paper-only, order execution is disabled, and every
API route requires a token, so exploitation needs a malicious upstream (signal-service or
Alpaca) or an authenticated caller. Still worth fixing before M2.

**Fix (needs approval, toolchain change):** `x/net` v0.60.0 requires Go 1.26 and `x/text`
v0.39.0 requires Go 1.25. The fix is:

1. `go.mod`: `go 1.26`; `go get golang.org/x/net@v0.60.0 golang.org/x/text@v0.39.0`; `go mod tidy`.
2. `bot-engine/Dockerfile`: `golang:1.22-alpine` → `golang:1.26-alpine`.
3. Re-run `go test ./...` and `make audit-go`.

**Resolved (BL-12a, 2026-11-25):** bot-engine moved to Go 1.26 with `x/net` v0.60.0,
`x/text` v0.42.0, `x/crypto` v0.57.0 and `x/sys` v0.48.0 (Docker builder `golang:1.26-alpine`).
govulncheck now reports **0 reachable vulnerabilities**; 3 in imported packages and 1 in a
required module remain unreached.
