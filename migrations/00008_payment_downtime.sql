-- +goose Up
-- docs/PHASE5_5_IMPLEMENTATION.md Unit Y: Razorpay's payment-downtime
-- webhooks (payment.downtime.started/.updated/.resolved) as a guardrail
-- input for the Decision Engine, the same table-ownership shape as
-- RECORD_STATE (docs/ARCHITECTURE.md section 10a): Decision Engine writes
-- it, nobody else does, and it is read fresh on every guardrail check
-- rather than cached, so a restart mid-outage loses nothing (the row is
-- already durable) and a resolved event takes effect the moment it commits.
--
-- One row per Razorpay downtime id, upserted across started -> updated ->
-- resolved rather than one row per event, so "is this instrument covered
-- right now" is always a lookup of the latest known state, not a scan of a
-- growing history. instrument_key is the single identifying value the API
-- Gateway extracts from Razorpay's `instrument` object (varies by method:
-- {"bank":"VIJB"} for netbanking, {"issuer":"SBIN","type":"credit"} or
-- {"network":"MC","type":"credit"} for a card), never the raw object
-- itself, so this table does not need to know Razorpay's per-method shapes.
CREATE TABLE payment_downtime (
    id             TEXT        PRIMARY KEY, -- Razorpay's down_xxx id
    method         TEXT        NOT NULL,
    instrument_key TEXT        NOT NULL,
    severity       TEXT        NOT NULL, -- open string: "high"/"medium" today, never validated against a closed list
    scheduled      BOOLEAN     NOT NULL,
    begin_at       TIMESTAMPTZ NOT NULL,
    end_at         TIMESTAMPTZ,           -- NULL while ongoing, matches Razorpay's own `end: null` shape
    resolved_at    TIMESTAMPTZ,           -- NULL until a payment.downtime.resolved event lands; NOT NULL means inactive regardless of begin_at/end_at
    created_at     TIMESTAMPTZ NOT NULL,
    updated_at     TIMESTAMPTZ NOT NULL
);

-- The guardrail's whole query shape: "is there an unresolved downtime for
-- this instrument". Partial on resolved_at IS NULL keeps the index small,
-- since a resolved row is never looked up by this path again.
CREATE INDEX payment_downtime_instrument_idx ON payment_downtime (instrument_key) WHERE resolved_at IS NULL;

-- +goose Down
DROP TABLE IF EXISTS payment_downtime;
