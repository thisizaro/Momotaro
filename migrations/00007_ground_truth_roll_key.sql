-- +goose Up
-- docs/DEMO_READINESS.md Unit AD, take two. The first pass (PR #99) keyed
-- SimulateOutcome's roll off (seed, record_id, attempt_number), which is a
-- deterministic function of its inputs but was never fed the same inputs
-- twice: record_id is uuid.NewString() at generation time in both
-- SeedBatch (demo/world-simulator/internal/server/seed.go) and
-- scripts/batchgen, fresh and random every run, seed or no seed. Two
-- batches seeded with the same seed therefore still rolled a different set
-- of outcomes, because the "same seed" reached the roll through a
-- different, unrepeatable record_id each time.
--
-- Record ids cannot become deterministic themselves: two batches seeded
-- with the same seed would then mint identical record.id values and
-- collide on this table's own primary key (record_id references record.id
-- 1:1). This column is the fix instead: a value chosen at generation time
-- from (seed, ordinal index within the batch) -- reproducible across two
-- runs of the same seed by construction, and independent of the record's
-- own (necessarily unique) id. SimulateOutcome now keys the roll off this
-- column rather than record_id.
--
-- NOT NULL with a default of '': existing rows (and anything that still
-- writes GROUND_TRUTH without setting it) fall back to record_id at read
-- time in Go, same as the empty-string default here signals "not set".
ALTER TABLE ground_truth ADD COLUMN roll_key TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE ground_truth DROP COLUMN IF EXISTS roll_key;
