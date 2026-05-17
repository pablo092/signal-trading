-- ════════════════════════════════════════════════════════════════
-- Trading Bot — PostgreSQL initial schema
-- Run automatically by postgres container on first start.
-- ════════════════════════════════════════════════════════════════

-- Extensions
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS "pg_stat_statements";

-- ── Signals ──────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS signals (
    id              UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    symbol          VARCHAR(10) NOT NULL,
    direction       VARCHAR(4)  NOT NULL CHECK (direction IN ('BUY','SELL','HOLD')),
    strength        VARCHAR(8)  NOT NULL CHECK (strength IN ('STRONG','MODERATE','WEAK')),
    price           NUMERIC(18,4) NOT NULL,
    confidence      NUMERIC(5,4)  NOT NULL CHECK (confidence BETWEEN 0 AND 1),
    reason          TEXT        NOT NULL,
    strategy_name   VARCHAR(100) NOT NULL,
    is_actionable   BOOLEAN     NOT NULL DEFAULT TRUE,
    generated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_signals_symbol       ON signals (symbol);
CREATE INDEX IF NOT EXISTS idx_signals_generated_at ON signals (generated_at DESC);

-- ── Orders ───────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS orders (
    id           UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    symbol       VARCHAR(10) NOT NULL,
    side         VARCHAR(4)  NOT NULL CHECK (side IN ('buy','sell')),
    type         VARCHAR(6)  NOT NULL CHECK (type IN ('market','limit')),
    quantity     NUMERIC(18,8) NOT NULL,
    limit_price  NUMERIC(18,4),
    status       VARCHAR(10) NOT NULL CHECK (status IN ('pending','submitted','filled','canceled','rejected')),
    broker_id    VARCHAR(100),
    filled_price NUMERIC(18,4),
    filled_at    TIMESTAMPTZ,
    strategy_id  VARCHAR(100) NOT NULL,
    signal_id    VARCHAR(100) NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_orders_symbol     ON orders (symbol);
CREATE INDEX IF NOT EXISTS idx_orders_status     ON orders (status);
CREATE INDEX IF NOT EXISTS idx_orders_created_at ON orders (created_at DESC);

-- ── Trades (filled orders) ────────────────────────────────────────
CREATE TABLE IF NOT EXISTS trades (
    id            UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    order_id      UUID        NOT NULL REFERENCES orders(id),
    symbol        VARCHAR(10) NOT NULL,
    side          VARCHAR(4)  NOT NULL,
    quantity      NUMERIC(18,8) NOT NULL,
    price         NUMERIC(18,4) NOT NULL,
    commission    NUMERIC(18,4) NOT NULL DEFAULT 0,
    realized_pnl  NUMERIC(18,4) NOT NULL DEFAULT 0,
    executed_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_trades_symbol      ON trades (symbol);
CREATE INDEX IF NOT EXISTS idx_trades_executed_at ON trades (executed_at DESC);

-- ── Auto-update updated_at ────────────────────────────────────────
CREATE OR REPLACE FUNCTION update_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER orders_updated_at
    BEFORE UPDATE ON orders
    FOR EACH ROW EXECUTE FUNCTION update_updated_at();
