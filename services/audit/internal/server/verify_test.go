package server

import (
	"testing"

	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

func snap(id string, hasState bool, current commonv1.RecordState, entries ...transition) recordSnapshot {
	return recordSnapshot{RecordID: id, HasState: hasState, CurrentState: current, Entries: entries}
}

func TestVerifyInvariantsCleanRecordsProduceZeroViolations(t *testing.T) {
	snapshots := []recordSnapshot{
		snap("rec-untouched", false, commonv1.RecordState_RECORD_STATE_UNSPECIFIED),
		snap("rec-recovered", true, commonv1.RecordState_RECORD_STATE_RECOVERED,
			transition{From: commonv1.RecordState_RECORD_STATE_NEW, To: commonv1.RecordState_RECORD_STATE_RECOVERED}),
		snap("rec-escalated", true, commonv1.RecordState_RECORD_STATE_ESCALATED,
			transition{From: commonv1.RecordState_RECORD_STATE_NEW, To: commonv1.RecordState_RECORD_STATE_ESCALATED}),
		snap("rec-multi-hop", true, commonv1.RecordState_RECORD_STATE_RECOVERED,
			transition{From: commonv1.RecordState_RECORD_STATE_NEW, To: commonv1.RecordState_RECORD_STATE_SCORING},
			transition{From: commonv1.RecordState_RECORD_STATE_SCORING, To: commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED},
			transition{From: commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED, To: commonv1.RecordState_RECORD_STATE_RETRYING},
			transition{From: commonv1.RecordState_RECORD_STATE_RETRYING, To: commonv1.RecordState_RECORD_STATE_RECOVERED}),
	}

	resp := verifyInvariants(snapshots)

	if resp.RecordsChecked != 4 {
		t.Errorf("RecordsChecked = %d, want 4", resp.RecordsChecked)
	}
	if resp.IncompleteAuditTrails != 0 {
		t.Errorf("IncompleteAuditTrails = %d, want 0", resp.IncompleteAuditTrails)
	}
	if resp.ImpossibleTransitions != 0 {
		t.Errorf("ImpossibleTransitions = %d, want 0", resp.ImpossibleTransitions)
	}
	if resp.StoppingRuleViolations != 0 {
		t.Errorf("StoppingRuleViolations = %d, want 0 (no caps exist yet)", resp.StoppingRuleViolations)
	}
	if len(resp.Examples) != 0 {
		t.Errorf("Examples = %v, want empty", resp.Examples)
	}
}

func TestVerifyInvariantsFlagsMissingAuditTrail(t *testing.T) {
	snapshots := []recordSnapshot{
		snap("rec-ghost", true, commonv1.RecordState_RECORD_STATE_RECOVERED),
	}

	resp := verifyInvariants(snapshots)

	if resp.IncompleteAuditTrails != 1 {
		t.Errorf("IncompleteAuditTrails = %d, want 1", resp.IncompleteAuditTrails)
	}
	if _, ok := resp.Examples["rec-ghost"]; !ok {
		t.Error("no example recorded for rec-ghost")
	}
}

func TestVerifyInvariantsFlagsStateMismatch(t *testing.T) {
	// record_state says Escalated, but the last logged transition says
	// Recovered: the two disagree, meaning a write happened outside the
	// WithTx transaction somewhere.
	snapshots := []recordSnapshot{
		snap("rec-mismatch", true, commonv1.RecordState_RECORD_STATE_ESCALATED,
			transition{From: commonv1.RecordState_RECORD_STATE_NEW, To: commonv1.RecordState_RECORD_STATE_RECOVERED}),
	}

	resp := verifyInvariants(snapshots)

	if resp.IncompleteAuditTrails != 1 {
		t.Errorf("IncompleteAuditTrails = %d, want 1", resp.IncompleteAuditTrails)
	}
}

func TestVerifyInvariantsFlagsImpossibleTransition(t *testing.T) {
	snapshots := []recordSnapshot{
		snap("rec-teleport", true, commonv1.RecordState_RECORD_STATE_NUDGED,
			transition{From: commonv1.RecordState_RECORD_STATE_NEW, To: commonv1.RecordState_RECORD_STATE_NUDGED}),
	}

	resp := verifyInvariants(snapshots)

	if resp.ImpossibleTransitions != 1 {
		t.Errorf("ImpossibleTransitions = %d, want 1", resp.ImpossibleTransitions)
	}
	if _, ok := resp.Examples["rec-teleport"]; !ok {
		t.Error("no example recorded for rec-teleport")
	}
}

func TestVerifyInvariantsFlagsBrokenChain(t *testing.T) {
	// Entry 1's from_state (Scoring) does not match entry 0's to_state
	// (RetryScheduled): a gap in the trail.
	snapshots := []recordSnapshot{
		snap("rec-gap", true, commonv1.RecordState_RECORD_STATE_RECOVERED,
			transition{From: commonv1.RecordState_RECORD_STATE_NEW, To: commonv1.RecordState_RECORD_STATE_SCORING},
			transition{From: commonv1.RecordState_RECORD_STATE_SCORING, To: commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED},
			transition{From: commonv1.RecordState_RECORD_STATE_SCORING, To: commonv1.RecordState_RECORD_STATE_RECOVERED}),
	}

	resp := verifyInvariants(snapshots)

	if resp.ImpossibleTransitions != 1 {
		t.Errorf("ImpossibleTransitions = %d, want 1", resp.ImpossibleTransitions)
	}
}

func TestVerifyInvariantsExamplesAreBounded(t *testing.T) {
	var snapshots []recordSnapshot
	for i := 0; i < maxExamples+10; i++ {
		snapshots = append(snapshots, snap(fmtID(i), true, commonv1.RecordState_RECORD_STATE_RECOVERED))
	}

	resp := verifyInvariants(snapshots)

	if int(resp.IncompleteAuditTrails) != len(snapshots) {
		t.Errorf("IncompleteAuditTrails = %d, want %d (every record counted even if not exemplified)", resp.IncompleteAuditTrails, len(snapshots))
	}
	if len(resp.Examples) != maxExamples {
		t.Errorf("len(Examples) = %d, want capped at %d", len(resp.Examples), maxExamples)
	}
}

func fmtID(i int) string {
	const letters = "0123456789abcdef"
	return "rec-" + string(letters[i%16]) + string(letters[(i/16)%16])
}

func TestVerifyInvariantsCountsUntouchedRecordsButNeverFlagsThem(t *testing.T) {
	snapshots := []recordSnapshot{
		snap("rec-fresh-1", false, commonv1.RecordState_RECORD_STATE_UNSPECIFIED),
		snap("rec-fresh-2", false, commonv1.RecordState_RECORD_STATE_UNSPECIFIED),
	}

	resp := verifyInvariants(snapshots)

	if resp.RecordsChecked != 2 {
		t.Errorf("RecordsChecked = %d, want 2", resp.RecordsChecked)
	}
	if resp.IncompleteAuditTrails != 0 || resp.ImpossibleTransitions != 0 {
		t.Errorf("a record with no record_state yet must never be flagged: %+v", resp)
	}
}
