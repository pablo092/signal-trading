-- M3 (BL-21): order intents (ADR-005 §5.3–5.4). The full intent is a JSONB
-- document; the columns that carry constraints are kept beside it.

CREATE TABLE order_intents (
    id                UUID        PRIMARY KEY,              -- also the order id and broker client order id
    status            TEXT        NOT NULL CHECK (status IN
                          ('pending_risk', 'pending_approval', 'approved', 'submitted',
                           'filled', 'rejected', 'canceled', 'expired')),
    actor_id          TEXT        NOT NULL,
    idempotency_key   TEXT        NOT NULL CHECK (idempotency_key ~ '^[A-Za-z0-9._:-]{8,128}$'),
    signal_id         TEXT        NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL,
    expires_at        TIMESTAMPTZ NOT NULL,
    updated_at        TIMESTAMPTZ NOT NULL,
    doc               JSONB       NOT NULL,                 -- current state
    creation_snapshot JSONB       NOT NULL,                 -- returned on idempotent replay
    CONSTRAINT order_intents_idempotency_uq UNIQUE (actor_id, idempotency_key)
);

-- At most one active intent per signal (ADR-005 §5.4).
CREATE UNIQUE INDEX order_intents_active_signal_uq ON order_intents (signal_id)
    WHERE status IN ('pending_risk', 'pending_approval', 'approved');

CREATE INDEX order_intents_status_idx  ON order_intents (status, created_at DESC);
CREATE INDEX order_intents_created_idx ON order_intents (created_at DESC);
CREATE INDEX order_intents_expiry_idx  ON order_intents (expires_at) WHERE status = 'pending_approval';
