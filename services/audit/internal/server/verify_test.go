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
			transition{From: commonv1.RecordState_RECORD_STATE_NEW, To: commonv1.RecordState_RECORD_STATE_SCORING},
			transition{From: commonv1.RecordState_RECORD_STATE_SCORING, To: commonv1.RecordState_RECORD_STATE_NUDGE_SCHEDULED},
			transition{From: commonv1.RecordState_RECORD_STATE_NUDGE_SCHEDULED, To: commonv1.RecordState_RECORD_STATE_NUDGED},
			transition{From: commonv1.RecordState_RECORD_STATE_NUDGED, To: commonv1.RecordState_RECORD_STATE_RECOVERED}),
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

// These are the trail shapes the live pipeline actually produces now that
// every classified record is routed through Scoring (docs/PHASE2_IMPLEMENTATION.md
// Unit M): the direct New -> RetryScheduled/NudgeScheduled/Recovered edges
// that docs/INCIDENTS.md 2026-08-23 carved out for the Phase 1 pipeline are
// gone, because Phase 1's unscored path no longer exists.
func TestVerifyInvariantsAcceptsRealTrails(t *testing.T) {
	newToScoring := transition{From: commonv1.RecordState_RECORD_STATE_NEW, To: commonv1.RecordState_RECORD_STATE_SCORING}
	scoredTo := func(to commonv1.RecordState) transition {
		return transition{From: commonv1.RecordState_RECORD_STATE_SCORING, To: to}
	}
	snapshots := []recordSnapshot{
		// Scored, retried, recovered.
		snap("rec-retry-recovered", true, commonv1.RecordState_RECORD_STATE_RECOVERED,
			newToScoring,
			scoredTo(commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED),
			transition{From: commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED, To: commonv1.RecordState_RECORD_STATE_RETRYING},
			transition{From: commonv1.RecordState_RECORD_STATE_RETRYING, To: commonv1.RecordState_RECORD_STATE_RECOVERED}),
		// Scored, retried, a failing execute escalates.
		snap("rec-retry-escalated", true, commonv1.RecordState_RECORD_STATE_ESCALATED,
			newToScoring,
			scoredTo(commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED),
			transition{From: commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED, To: commonv1.RecordState_RECORD_STATE_RETRYING},
			transition{From: commonv1.RecordState_RECORD_STATE_RETRYING, To: commonv1.RecordState_RECORD_STATE_ESCALATED}),
		// Scored, a nudge parks in Nudged awaiting the Phase 5 delayed outcome.
		snap("rec-nudge-parked", true, commonv1.RecordState_RECORD_STATE_NUDGED,
			newToScoring,
			scoredTo(commonv1.RecordState_RECORD_STATE_NUDGE_SCHEDULED),
			transition{From: commonv1.RecordState_RECORD_STATE_NUDGE_SCHEDULED, To: commonv1.RecordState_RECORD_STATE_NUDGED}),
	}

	resp := verifyInvariants(snapshots)

	if resp.RecordsChecked != 3 {
		t.Errorf("RecordsChecked = %d, want 3", resp.RecordsChecked)
	}
	if resp.ImpossibleTransitions != 0 || resp.IncompleteAuditTrails != 0 {
		t.Errorf("the pipeline's own normal output was reported as a violation: %+v", resp)
	}
}

func TestTrailComplete(t *testing.T) {
	tests := []struct {
		name string
		snap recordSnapshot
		want bool
	}{
		{
			"a sound trail through Scoring",
			snap("a", true, commonv1.RecordState_RECORD_STATE_RETRYING,
				transition{From: commonv1.RecordState_RECORD_STATE_NEW, To: commonv1.RecordState_RECORD_STATE_SCORING},
				transition{From: commonv1.RecordState_RECORD_STATE_SCORING, To: commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED},
				transition{From: commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED, To: commonv1.RecordState_RECORD_STATE_RETRYING}),
			true,
		},
		{
			// Ingested, not yet picked up by Decision Engine. No state change
			// has happened, so there is no trail that could be missing one.
			"untouched record",
			snap("b", false, commonv1.RecordState_RECORD_STATE_UNSPECIFIED),
			true,
		},
		{
			"state with no entries at all",
			snap("c", true, commonv1.RecordState_RECORD_STATE_RECOVERED),
			false,
		},
		{
			"last entry disagrees with current_state",
			snap("d", true, commonv1.RecordState_RECORD_STATE_ESCALATED,
				transition{From: commonv1.RecordState_RECORD_STATE_NEW, To: commonv1.RecordState_RECORD_STATE_RECOVERED}),
			false,
		},
		{
			"an edge outside the state machine",
			snap("e", true, commonv1.RecordState_RECORD_STATE_NUDGED,
				transition{From: commonv1.RecordState_RECORD_STATE_NEW, To: commonv1.RecordState_RECORD_STATE_NUDGED}),
			false,
		},
		{
			"a broken chain between two otherwise-legal edges",
			snap("f", true, commonv1.RecordState_RECORD_STATE_RECOVERED,
				transition{From: commonv1.RecordState_RECORD_STATE_NEW, To: commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED},
				transition{From: commonv1.RecordState_RECORD_STATE_NUDGED, To: commonv1.RecordState_RECORD_STATE_RECOVERED}),
			false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := trailComplete(tc.snap); got != tc.want {
				t.Errorf("trailComplete = %v, want %v", got, tc.want)
			}
		})
	}
}
