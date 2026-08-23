-- +goose Up
-- The Decision Engine's scheduler worker (docs/ARCHITECTURE.md section 7a)
-- resumes a record when due_at passes, but the state alone does not say
-- which action to execute: RECORD_STATE_NUDGE_SCHEDULED covers both
-- ACTION_TYPE_NUDGE_METHOD_UPDATE and ACTION_TYPE_NUDGE_REMINDER. Without
-- somewhere to remember the specific action chosen at scheduling time, the
-- scheduler has no correct way to resume it. Additive only, per
-- docs/ARCHITECTURE.md section 12a.
--
-- Nullable: only meaningful while a record sits in a waiting state
-- (RETRY_SCHEDULED / NUDGE_SCHEDULED); irrelevant once terminal.
ALTER TABLE record_state ADD COLUMN pending_action TEXT;

-- +goose Down
ALTER TABLE record_state DROP COLUMN IF EXISTS pending_action;
