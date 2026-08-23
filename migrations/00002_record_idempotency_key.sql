-- +goose Up
-- Ingestion.SubmitEvent (proto/ingestion/v1/ingestion.proto) accepts an
-- optional idempotency_key so a redelivered webhook cannot create a
-- duplicate record: "two submissions with the same key are the same
-- event." That check needs something durable to key on, which the initial
-- schema did not have. Additive only, per docs/ARCHITECTURE.md section 12a.
--
-- Nullable because SubmitBatch records never carry one (batch submission has
-- no webhook redelivery to guard against). The partial unique index only
-- constrains rows that actually set a key, so those NULLs never collide with
-- each other.
ALTER TABLE record ADD COLUMN idempotency_key TEXT;

CREATE UNIQUE INDEX record_idempotency_key_idx
    ON record (idempotency_key)
    WHERE idempotency_key IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS record_idempotency_key_idx;
ALTER TABLE record DROP COLUMN IF EXISTS idempotency_key;
