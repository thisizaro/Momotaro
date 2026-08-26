-- +goose Up
-- Phase 2 Unit G (docs/PHASE2_IMPLEMENTATION.md): the economics scorer
-- computes an EV and a recovery probability at Scoring time
-- (services/decision-engine/internal/economics/score.go), but Execute does
-- not happen until the scheduler claims the record later, sometimes minutes
-- or a whole salary window afterward. Without somewhere to hold the
-- snapshot between those two moments, the Decision Engine has no way to
-- tell the Executor what it decided, and INTERVENTION_ATTEMPT's own
-- ev_score_at_decision/p_recovery_at_decision columns (migration 00001)
-- stay permanently unpopulated. Nullable: only meaningful while a record is
-- scheduled with a pending_action (RETRY_SCHEDULED / NUDGE_SCHEDULED, same
-- lifetime as that column, migration 00003); irrelevant once terminal.
-- Additive only, per docs/ARCHITECTURE.md section 12a.
ALTER TABLE record_state ADD COLUMN ev_score_at_decision DOUBLE PRECISION;
ALTER TABLE record_state ADD COLUMN p_recovery_at_decision DOUBLE PRECISION;

-- +goose Down
ALTER TABLE record_state DROP COLUMN IF EXISTS ev_score_at_decision;
ALTER TABLE record_state DROP COLUMN IF EXISTS p_recovery_at_decision;
