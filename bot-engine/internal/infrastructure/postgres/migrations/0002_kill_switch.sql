-- BL-19: the persisted kill-switch flag (ADR-005 §5.7). Exactly one row.

CREATE TABLE kill_switch (
    id         SMALLINT    PRIMARY KEY CHECK (id = 1),
    engaged    BOOLEAN     NOT NULL,
    reason     TEXT        NOT NULL DEFAULT '',
    updated_by TEXT        NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO kill_switch (id, engaged, reason, updated_by) VALUES (1, false, 'initial', 'migration');
