-- +goose Up
-- Initial schema. Everything docs/ARCHITECTURE.md section 10 describes, in
-- one migration, so nothing load-bearing gets retrofitted later.
--
-- Money is BIGINT paise throughout. Never NUMERIC, never a float: our
-- headline metric is a money figure and floating point on currency produces
-- rounding errors that quietly corrupt a total.

-- A submitted group of records. Exists so every report scopes to one run
-- rather than to the lifetime total of everything ever ingested.
CREATE TABLE batch (
    id            UUID PRIMARY KEY,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    total_records INTEGER     NOT NULL DEFAULT 0,
    -- Provenance, e.g. 'synthetic-demo', 'webhook', 'backfill-2026-08'.
    source        TEXT        NOT NULL
);

-- A revenue-at-risk record. Owned by the Ingestion service.
CREATE TABLE record (
    id             UUID PRIMARY KEY,
    batch_id       UUID        NOT NULL REFERENCES batch(id),
    type           TEXT        NOT NULL,
    amount_paise   BIGINT      NOT NULL CHECK (amount_paise > 0),
    currency       TEXT        NOT NULL DEFAULT 'INR',
    -- Raw code from the upstream rail. The classifier's INPUT, not output.
    failure_code   TEXT        NOT NULL,
    -- Opaque handle for the payment instrument, used to find prior outcomes
    -- on the same card/mandate. Never the instrument itself.
    instrument_ref TEXT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX record_batch_idx      ON record (batch_id);
CREATE INDEX record_instrument_idx ON record (instrument_ref) WHERE instrument_ref IS NOT NULL;

-- Current state, one row per record. Owned by the Decision Engine.
CREATE TABLE record_state (
    record_id         UUID PRIMARY KEY REFERENCES record(id),
    current_state     TEXT        NOT NULL,
    attempt_count     INTEGER     NOT NULL DEFAULT 0,
    root_cause_bucket TEXT,
    last_action_at    TIMESTAMPTZ,
    -- When this record next becomes actionable. NULL means not waiting.
    -- The scheduler worker claims on this column; without it no cooldown or
    -- salary-window retry would ever fire. See ARCHITECTURE.md section 7a.
    due_at            TIMESTAMPTZ,
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Partial index supporting the scheduler's FOR UPDATE SKIP LOCKED claim.
-- Partial because only waiting records are ever claimable, so the index
-- stays small no matter how many terminal records accumulate.
CREATE INDEX record_state_due_idx
    ON record_state (due_at)
    WHERE due_at IS NOT NULL
      AND current_state IN ('RECORD_STATE_RETRY_SCHEDULED', 'RECORD_STATE_NUDGE_SCHEDULED');

CREATE INDEX record_state_current_idx ON record_state (current_state);

-- One executed intervention. Owned by the Executor.
CREATE TABLE intervention_attempt (
    id                     UUID PRIMARY KEY,
    record_id              UUID        NOT NULL REFERENCES record(id),
    attempt_number         INTEGER     NOT NULL CHECK (attempt_number > 0),
    action_type            TEXT        NOT NULL,
    outcome                TEXT        NOT NULL,
    executed_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- What this attempt cost. Makes 'net recovered' a measurement rather
    -- than an estimate.
    cost_paise             BIGINT      NOT NULL DEFAULT 0 CHECK (cost_paise >= 0),
    -- Snapshot of the economics at decision time, so the trail can answer
    -- 'why did you think this was worth spending?' even after the
    -- probability priors have been recalibrated.
    ev_score_at_decision   DOUBLE PRECISION,
    p_recovery_at_decision DOUBLE PRECISION,
    -- The actual message sent, for nudge actions. Lets the demo show real
    -- Hinglish wording instead of describing it.
    message_text           TEXT,
    message_source         TEXT,
    failure_code           TEXT,

    -- THE durable idempotency guarantee (ARCHITECTURE.md section 11). The
    -- Executor inserts this row BEFORE performing the side effect; a
    -- duplicate violates this constraint and the previously recorded outcome
    -- is returned instead of re-executing. Unlike a Redis key, this never
    -- expires.
    CONSTRAINT intervention_attempt_idem UNIQUE (record_id, attempt_number)
);
CREATE INDEX intervention_attempt_record_idx ON intervention_attempt (record_id);

-- Append-only history. THE source of truth for what happened.
--
-- Written in the SAME TRANSACTION as the state change it describes, by the
-- service that owns that change. Kafka's audit.events is a notification
-- stream, never a system of record. See ARCHITECTURE.md section 10a.
CREATE TABLE audit_entry (
    id             BIGSERIAL PRIMARY KEY,
    record_id      UUID        NOT NULL REFERENCES record(id),
    batch_id       UUID        NOT NULL REFERENCES batch(id),
    ts             TIMESTAMPTZ NOT NULL DEFAULT now(),
    from_state     TEXT        NOT NULL,
    to_state       TEXT        NOT NULL,
    reason         TEXT        NOT NULL,
    -- The classifier's verbatim rationale. Never paraphrased or truncated:
    -- this is what makes the agent's reasoning auditable by a human.
    rationale      TEXT,
    source         TEXT,
    actor          TEXT        NOT NULL DEFAULT 'system',
    attempt_number INTEGER,
    cost_paise     BIGINT,
    message_text   TEXT
);
CREATE INDEX audit_entry_record_idx ON audit_entry (record_id, ts);
CREATE INDEX audit_entry_batch_idx  ON audit_entry (batch_id);

-- The sealed answer key. DEMO ONLY.
--
-- Readable ONLY by the World Simulator and the Reporting service's accuracy
-- scorer. The Decision Engine and Classifier must have NO query path here:
-- an agent that can see which records are recoverable makes every accuracy
-- and recovery number in the demo meaningless. Enforced in code and by a
-- test, and modelled as its own table so the boundary is structurally
-- obvious. See ARCHITECTURE.md sections 5a, 6.
CREATE TABLE ground_truth (
    record_id           UUID PRIMARY KEY REFERENCES record(id),
    true_bucket         TEXT             NOT NULL,
    -- Probability this record recovers given the correct intervention.
    recovery_probability DOUBLE PRECISION NOT NULL
        CHECK (recovery_probability >= 0 AND recovery_probability <= 1),
    -- Probability it recovers given a WRONG intervention. Usually near zero;
    -- this is what makes choosing correctly actually matter.
    wrong_action_probability DOUBLE PRECISION NOT NULL DEFAULT 0
        CHECK (wrong_action_probability >= 0 AND wrong_action_probability <= 1),
    -- How long a customer takes to react to a nudge, modelling the delayed
    -- outcome path.
    response_delay_seconds INTEGER NOT NULL DEFAULT 0 CHECK (response_delay_seconds >= 0)
);

-- +goose Down
DROP TABLE IF EXISTS ground_truth;
DROP TABLE IF EXISTS audit_entry;
DROP TABLE IF EXISTS intervention_attempt;
DROP TABLE IF EXISTS record_state;
DROP TABLE IF EXISTS record;
DROP TABLE IF EXISTS batch;
