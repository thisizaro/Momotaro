package server

import commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"

// transition is one edge in a record's audit trail: it moved from From to
// To. Used both as the state-machine's edge key and as the per-entry shape
// store.go reads out of AUDIT_ENTRY.
type transition struct {
	From, To commonv1.RecordState
}

// allowedTransitions is the state machine from docs/ARCHITECTURE.md
// section 7. This is what impossibleTransition (verify.go) checks every
// recorded transition against.
var allowedTransitions = map[transition]bool{
	{commonv1.RecordState_RECORD_STATE_NEW, commonv1.RecordState_RECORD_STATE_SCORING}:   true,
	{commonv1.RecordState_RECORD_STATE_NEW, commonv1.RecordState_RECORD_STATE_ESCALATED}: true,

	{commonv1.RecordState_RECORD_STATE_SCORING, commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED}:   true,
	{commonv1.RecordState_RECORD_STATE_SCORING, commonv1.RecordState_RECORD_STATE_NUDGE_SCHEDULED}:   true,
	{commonv1.RecordState_RECORD_STATE_SCORING, commonv1.RecordState_RECORD_STATE_CLOSED_UNECONOMIC}: true,
	// A re-entry to Scoring (Retrying -> Scoring or Nudged -> Scoring, Unit
	// E's loop) can find every spending action guardrail-blocked -- the
	// retry budget and the contact cap are both finite, and re-scoring is
	// exactly what runs them down. permittedOrEscalate (guardrails.go) then
	// falls back to ESCALATE from within the same Scoring step, so this
	// edge is a real, permanent output of the guardrails, not a temporary
	// one. Missing until Unit H's batch-invariants test actually pushed a
	// record's history to the cap: nothing before it ever exercised a
	// record retrying or being contacted enough times to exhaust a budget
	// (docs/PHASE2_IMPLEMENTATION.md Unit H, docs/INCIDENTS.md).
	{commonv1.RecordState_RECORD_STATE_SCORING, commonv1.RecordState_RECORD_STATE_ESCALATED}: true,

	{commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED, commonv1.RecordState_RECORD_STATE_RETRYING}: true,
	{commonv1.RecordState_RECORD_STATE_RETRYING, commonv1.RecordState_RECORD_STATE_RECOVERED}:       true,
	{commonv1.RecordState_RECORD_STATE_RETRYING, commonv1.RecordState_RECORD_STATE_SCORING}:         true,
	{commonv1.RecordState_RECORD_STATE_RETRYING, commonv1.RecordState_RECORD_STATE_ESCALATED}:       true,

	{commonv1.RecordState_RECORD_STATE_NUDGE_SCHEDULED, commonv1.RecordState_RECORD_STATE_NUDGED}: true,

	// Nudged -> Nudged is a real edge, not a temporary one, and not a no-op.
	// The scheduler claims a due nudge into Nudged before executing it, and a
	// sent nudge's outcome is PENDING, which is also Nudged because the
	// customer has not answered yet. So the second entry records that the
	// nudge actually went out, carrying its attempt number and what it cost;
	// suppressing it would drop that spend from the history the trail is
	// supposed to be the source of truth for. Found by the Phase 1 smoke
	// test, which is the only thing that exercises the nudge path end to end
	// (docs/INCIDENTS.md 2026-08-23).
	{commonv1.RecordState_RECORD_STATE_NUDGED, commonv1.RecordState_RECORD_STATE_NUDGED}:    true,
	{commonv1.RecordState_RECORD_STATE_NUDGED, commonv1.RecordState_RECORD_STATE_RECOVERED}: true,
	{commonv1.RecordState_RECORD_STATE_NUDGED, commonv1.RecordState_RECORD_STATE_SCORING}:   true,
	{commonv1.RecordState_RECORD_STATE_NUDGED, commonv1.RecordState_RECORD_STATE_ESCALATED}: true,
}

// isAllowedTransition reports whether from -> to is a valid edge in the
// state machine.
func isAllowedTransition(from, to commonv1.RecordState) bool {
	return allowedTransitions[transition{From: from, To: to}]
}
