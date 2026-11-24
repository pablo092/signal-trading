# Test Coverage Report — M0

All results below come from commands actually run on 2026-11-24 on the audit machine (Windows 11, PowerShell/Git Bash). Nothing was installed or upgraded. **No test contacted Alpaca or any external service.**

## 1. Environment

| Tool | Found | Required by project | Gap |
|---|---|---|---|
| git | 2.38.0 | — | — |
| Docker | 29.9.3 | — | — |
| Docker Compose | v5.7.0 | v2+ | — |
| Go | 1.22.5 | 1.22 (`bot-engine/go.mod:3`) | — |
| Python | 3.10.8 | ≥ 3.11 (`signal-service/pyproject.toml:9`) | **Version too old** |
| Python packages | pandas 2.2.2, numpy 1.26.4 only | fastapi, pydantic, pydantic-settings, structlog, alpaca-py, httpx, prometheus-client, pytest, pytest-asyncio, pytest-cov | **Missing** |
| Flutter / Dart | Not on PATH; not in common install locations | Dart SDK ≥ 3.3 (`app/pubspec.yaml:7`) | **Missing** |
| staticcheck / ruff / mypy | Not checked (not installed via project) | Used by Makefile/CI | Not verified |

## 2. Results

### Go — bot-engine

```
$ cd bot-engine && go test ./... -count=1
ok   .../internal/application/usecases          0.857s
ok   .../internal/infrastructure/circuitbreaker 1.364s
(11 packages: [no test files])
exit 0

$ go vet ./...           → exit 0, no findings
$ go build ./...         → exit 0
$ go test -cover ./...
  usecases        61.8%
  circuitbreaker  97.4%
  all other packages 0.0% (main, eventbus, entities, broker, config,
  handlers, middleware, messaging, repository, signalclient)
```

`-race` was **not** run. The CI/Makefile use it, and it requires CGO and a C toolchain, which were not verified here. A race is expected in `messaging/ws_hub.go` (TD-11), but no test exercises it.

### Python — signal-service

```
$ cd signal-service && python -m pytest
NOT EXECUTED: Python 3.10.8 < 3.11 and required packages are not installed.
```

Per the M0 rules, dependencies were not installed. **Static analysis** of `tests/test_strategies.py` against `src/`, however, predicts the following. This is **predicted, not observed**:

| Class | Tests | Predicted fail | Reason |
|---|---|---|---|
| `TestRSIStrategy` | 14 | 3 | `test_missing_column_raises` (no column validation, `KeyError` instead of `ValueError`); `test_neutral_produces_hold` and `test_strong_oversold_higher_confidence` (`make_bars` has no `trend`/`noise` kwargs) |
| `TestMovingAverageCrossStrategy` | 9 | 4 | `test_name_format`, `test_invalid_ma_type_raises`, `test_sma_type_works` (no `ma_type` param); `test_no_crossover_produces_hold` (`make_bars` kwargs) |
| `TestStrategyFactory` | 7 | 4 | `test_create_case_insensitive`, `test_unknown_strategy_raises` (message), `test_available_strategies_returns_list` (property called), `test_register_custom_strategy` (no `register`) |
| **Total** | **30** | **11** | |

Coverage would be far below the 80 % gate configured in `Makefile:58` and CI. Only `src/application/strategies/*` and value objects are exercised. `api/`, `main.py`, `infrastructure/`, and `application/generate_signal.py` have no tests.

**To execute safely** (requires approval to create a venv / install packages):

```powershell
cd signal-service
py -3.11 -m venv .venv            # requires Python 3.11 installed
.\.venv\Scripts\Activate.ps1
pip install -r requirements.txt   # pyproject install may fail, see TD-08
$env:ALPACA_API_KEY="PKTEST000000000"; $env:ALPACA_SECRET_KEY="dummy"
python -m pytest --cov=src --cov-report=term-missing
```

The strategy tests do not instantiate the Alpaca client, so dummy keys are sufficient and no network call is made.

### Flutter — app

```
$ flutter pub get / flutter analyze / flutter test
NOT EXECUTED: Flutter SDK not installed.
```

Tests present: `test/domain_test.dart`, `test/signal_bloc_test.dart`, `test/trading_widgets_test.dart`, `test/widget_test.dart`. The last commit (`37c63ff`) says they were rewritten, which could not be confirmed. Local `app/build/` and `app/pubspec.lock` exist, so the SDK was available at some point.

### Infrastructure

```
$ docker compose config --quiet                         → exit 0
$ docker compose -f signal-service/docker-compose.yml config --quiet
  → exit 0, warnings: ALPACA_* unset; `version` attribute obsolete
```

The stack was **not** started (per M0 rules: the unauthenticated order endpoint and unguarded live mode, SEC-01/02, make that unjustified for an audit).

### CI

```
$ gh run list
4 runs (2026-05-17), all "failure" in 3–6 s:
"The job was not started because your account is locked due to a billing issue."
```

No commit has ever passed CI.

## 3. Coverage gaps (prioritized)

| Gap | Risk | Backlog |
|---|---|---|
| Order path HTTP handler + broker adapter: 0 % | Order regressions undetected | BL-07 |
| WebSocket hub: 0 %, no `-race` test | Crashes and races | BL-15 |
| signal-service routes/adapter: 0 % | API contract regressions | BL-04 |
| No contract tests signal-service ↔ bot-engine ↔ app | Silent drift (TD-12 already happened) | BL-16 |
| Risk rules (`entities/risk_rules.go`): 0 % | Safety logic untested | BL-09 |
| No integration test with a fake broker | Can't test execution safely | BL-07 |
