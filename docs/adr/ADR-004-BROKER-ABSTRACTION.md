# ADR-004 — Broker Abstraction

| Field | Value |
|---|---|
| Status | **Proposed** |
| Date | 2026-11-24 |
| Deciders | Repository owner |
| Related | TD-02, TD-06, TD-21, ADR-005 |

## Context
bot-engine already defines `ports.BrokerPort` (`bot-engine/internal/domain/ports/ports.go:36-49`) with a single Alpaca implementation. The port has defects:
- `GetOrderStatus` returns `(status, float64)`, which is documented as a fill price but implemented as filled quantity (`alpaca.go:81-83`).
- Money is `float64` (TD-21).
- No `client_order_id` is passed.
- The adapter does not know whether it is paper or live (TD-02).
- Methods ignore `ctx`.

signal-service separately uses Alpaca **market data** through its own port `IMarketDataRepository` (`signal-service/src/domain/ports.py:61`).

## Problem
Define the broker interface needed to run Alpaca safely and to support tests, dry-run, and future brokers without leaking broker specifics into the domain.

## Options considered
1. **Evolve the existing `BrokerPort`** (v2) inside bot-engine, and add adapters.
2. **Adopt a third-party multi-broker library.**
3. **A separate broker-gateway microservice.**

## Decision (proposed)
Option 1. Define `BrokerPort` v2 in bot-engine:

```go
type BrokerPort interface {
    Capabilities() BrokerCapabilities            // paper/live, shorting, fractional, order types, extended hours
    Mode() TradingMode                           // verified at construction; Paper | Live
    SubmitOrder(ctx context.Context, req SubmitOrderRequest) (BrokerOrder, error) // req.ClientOrderID required
    CancelOrder(ctx context.Context, brokerOrderID string) error
    CancelAllOrders(ctx context.Context) error   // used by the kill switch
    GetOrder(ctx context.Context, brokerOrderID string) (BrokerOrder, error)      // status, filled qty, avg fill price
    GetOrderByClientID(ctx context.Context, clientOrderID string) (BrokerOrder, error) // idempotent recovery
    GetAccount(ctx context.Context) (Account, error)   // equity, buying power, day PnL (decimal)
    GetPositions(ctx context.Context) ([]Position, error)
    GetClock(ctx context.Context) (MarketClock, error)
    StreamTradeUpdates(ctx context.Context) (<-chan TradeUpdate, error) // for the reconciler
}
```

Adapters:
- `AlpacaAdapter`: refuses construction unless the verified mode matches config (ADR-005). It enforces a per-call timeout through `ctx`, and maps errors to typed domain errors (`ErrRejected`, `ErrInsufficientFunds`, `ErrRateLimited`, `ErrUnavailable`).
- `FakeBroker`: deterministic in-memory broker for unit/integration tests and `DRY_RUN`, with configurable fills, latency, and failures.
- Future brokers: implement the same port and declare `Capabilities()`.

Domain types use `decimal.Decimal` for price, quantity, and money.

Market data stays a separate port in signal-service. It is **not** merged into `BrokerPort`.

## Rationale
- The existing port is close; evolving it is cheaper than replacing it.
- `ClientOrderID` plus `GetOrderByClientID` provide broker-level idempotency and crash recovery (ADR-005).
- `Capabilities()` lets the risk engine reject unsupported operations (e.g. shorting) before they reach the broker.
- A fake broker is the only safe way to test the execution path. There is no test of it today (TEST_COVERAGE §3).

## Consequences
- `PlaceOrderUseCase` and the handlers must be updated. The existing tests' mocks change.
- Migration from `float64` to `decimal` touches the domain entities.
- Tests no longer need Alpaca credentials (CI benefit).

## Security implications
- Mode verification at construction removes the "one env var to live" risk (SEC-02).
- `CancelAllOrders` is a safety primitive and must require `admin:killswitch` (ADR-003).
- Typed errors prevent raw broker messages from leaking to callers (SEC-10).

## Alternatives rejected
- **Third-party multi-broker library:** adds an opaque dependency at the most sensitive boundary, and is not needed for one broker.
- **Separate broker-gateway service:** adds a network hop and a deployable, and conflicts with the "reduce complexity" principle. The port already gives the isolation.

## Open questions
- Is shorting ever in scope? This determines the default for `Capabilities().Shorting` in risk checks.
- Are fractional shares expected? Current sizing produces fractional quantities (`risk_rules.go:51-57`).
