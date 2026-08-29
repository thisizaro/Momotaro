-- +goose Up
-- Phase 5 Unit M (docs/PHASE5_IMPLEMENTATION.md): "every money action
-- explainable" (docs/PRD.md section 0) today explains the winner and
-- nothing else. economics.Model.ScoreAll and the guardrail verdict's
-- blocked map are both computed and thrown away microseconds after being
-- used to pick one action. This column persists the comparison, so the
-- audit trail can answer "why not the alternatives" for a specific scoring
-- decision, not just "why this one".
--
-- JSONB, not a new table: one scoring decision produces a handful of
-- candidates plus a handful of blocked actions, always small and always
-- read as a whole alongside the audit_entry row it belongs to, never
-- queried or joined on its own.
--
-- Nullable, and NULL on purpose for most rows: only the audit_entry row for
-- the transition leaving RECORD_STATE_SCORING (docs/PHASE5_IMPLEMENTATION.md
-- Unit M's own note on this) was preceded by an actual comparison; every
-- other transition (a classification, a claim, an execution outcome) has
-- nothing to attach here. Additive only, per docs/ARCHITECTURE.md section
-- 12a.
ALTER TABLE audit_entry ADD COLUMN decision_trace JSONB;

-- +goose Down
ALTER TABLE audit_entry DROP COLUMN IF EXISTS decision_trace;
