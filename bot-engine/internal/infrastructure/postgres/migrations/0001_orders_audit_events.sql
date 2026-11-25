-- BL-14: durable orders and the append-only audit log (TD-05, ADR-005 §5.8).
-- Money and quantities are NUMERIC; the Go side sends and reads them as text
-- so no precision is lost to floats.

CREATE TABLE orders (
    id           UUID          PRIMARY KEY,                 -- also the broker client order ID
    symbol       TEXT          NOT NULL,
    side         TEXT          NOT NULL CHECK (side IN ('buy', 'sell')),
    type         TEXT          NOT NULL CHECK (type IN ('market', 'limit')),
    quantity     NUMERIC(30,9) NOT NULL CHECK (quantity > 0),
    limit_price  NUMERIC(30,9)          CHECK (limit_price > 0),
    status       TEXT          NOT NULL CHECK (status IN
                     ('pending', 'submitted', 'partially_filled', 'filled', 'canceled', 'rejected', 'expired')),
    broker_id    TEXT,
    filled_qty   NUMERIC(30,9) NOT NULL DEFAULT 0 CHECK (filled_qty >= 0),
    filled_price NUMERIC(30,9),
    filled_at    TIMESTAMPTZ,
    strategy_id  TEXT          NOT NULL,
    signal_id    TEXT          NOT NULL,
    created_at   TIMESTAMPTZ   NOT NULL,
    updated_at   TIMESTAMPTZ   NOT NULL
);

CREATE INDEX orders_symbol_idx     ON orders (symbol, created_at);
CREATE INDEX orders_status_idx     ON orders (status);
CREATE UNIQUE INDEX orders_broker_id_uq ON orders (broker_id) WHERE broker_id IS NOT NULL;

CREATE TABLE audit_events (
    id             BIGINT      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    occurred_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    actor_id       TEXT        NOT NULL,
    actor_type     TEXT        NOT NULL,
    action         TEXT        NOT NULL,
    target         TEXT        NOT NULL DEFAULT '',
    result         TEXT        NOT NULL,
    request_id     TEXT        NOT NULL DEFAULT '',
    payload        JSONB       NOT NULL DEFAULT '{}'::jsonb,
    payload_sha256 TEXT        NOT NULL CHECK (payload_sha256 ~ '^[0-9a-f]{64}$')
);

CREATE INDEX audit_events_occurred_idx ON audit_events (occurred_at);
CREATE INDEX audit_events_target_idx   ON audit_events (target) WHERE target <> '';

-- Append-only: refuse UPDATE, DELETE and TRUNCATE, whoever the caller is.
CREATE FUNCTION audit_events_append_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'audit_events is append-only (% refused)', TG_OP
        USING ERRCODE = 'insufficient_privilege';
END;
$$;

CREATE TRIGGER audit_events_no_update_delete
    BEFORE UPDATE OR DELETE ON audit_events
    FOR EACH ROW EXECUTE FUNCTION audit_events_append_only();

CREATE TRIGGER audit_events_no_truncate
    BEFORE TRUNCATE ON audit_events
    FOR EACH STATEMENT EXECUTE FUNCTION audit_events_append_only();
