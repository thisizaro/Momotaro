package server

import (
	"testing"

	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

func TestIsAllowedTransitionAcceptsEveryFormalEdge(t *testing.T) {
	// Every edge from docs/ARCHITECTURE.md section 7's state diagram.
	formal := []transition{
		{commonv1.RecordState_RECORD_STATE_NEW, commonv1.RecordState_RECORD_STATE_SCORING},
		{commonv1.RecordState_RECORD_STATE_NEW, commonv1.RecordState_RECORD_STATE_ESCALATED},
		{commonv1.RecordState_RECORD_STATE_SCORING, commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED},
		{commonv1.RecordState_RECORD_STATE_SCORING, commonv1.RecordState_RECORD_STATE_NUDGE_SCHEDULED},
		{commonv1.RecordState_RECORD_STATE_SCORING, commonv1.RecordState_RECORD_STATE_CLOSED_UNECONOMIC},
		{commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED, commonv1.RecordState_RECORD_STATE_RETRYING},
		{commonv1.RecordState_RECORD_STATE_RETRYING, commonv1.RecordState_RECORD_STATE_RECOVERED},
		{commonv1.RecordState_RECORD_STATE_RETRYING, commonv1.RecordState_RECORD_STATE_SCORING},
		{commonv1.RecordState_RECORD_STATE_RETRYING, commonv1.RecordState_RECORD_STATE_ESCALATED},
		{commonv1.RecordState_RECORD_STATE_NUDGE_SCHEDULED, commonv1.RecordState_RECORD_STATE_NUDGED},
		{commonv1.RecordState_RECORD_STATE_NUDGED, commonv1.RecordState_RECORD_STATE_RECOVERED},
		{commonv1.RecordState_RECORD_STATE_NUDGED, commonv1.RecordState_RECORD_STATE_SCORING},
		{commonv1.RecordState_RECORD_STATE_NUDGED, commonv1.RecordState_RECORD_STATE_ESCALATED},
	}
	for _, tr := range formal {
		if !isAllowedTransition(tr.From, tr.To) {
			t.Errorf("formal edge %s -> %s rejected", tr.From, tr.To)
		}
	}
}

// docs/DECISIONS.md: the walking skeleton collapses New straight to
// Recovered, skipping Scoring/RetryScheduled/Retrying because neither the
// economics scorer nor the scheduler worker exist yet. The verifier must
// allow this ONE temporary edge or it would flag every record the current
// system produces as an invariant violation.
func TestIsAllowedTransitionAcceptsTheTemporarySkeletonEdge(t *testing.T) {
	if !isAllowedTransition(commonv1.RecordState_RECORD_STATE_NEW, commonv1.RecordState_RECORD_STATE_RECOVERED) {
		t.Error("New -> Recovered rejected, but this is what the walking skeleton actually produces")
	}
}

func TestIsAllowedTransitionRejectsInvalidEdges(t *testing.T) {
	tests := []struct {
		name     string
		from, to commonv1.RecordState
	}{
		{"skips the whole machine", commonv1.RecordState_RECORD_STATE_NEW, commonv1.RecordState_RECORD_STATE_NUDGED},
		{"terminal state has no outgoing edges", commonv1.RecordState_RECORD_STATE_RECOVERED, commonv1.RecordState_RECORD_STATE_NEW},
		{"escalated cannot un-escalate", commonv1.RecordState_RECORD_STATE_ESCALATED, commonv1.RecordState_RECORD_STATE_SCORING},
		{"reversed edge", commonv1.RecordState_RECORD_STATE_RETRYING, commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED},
		{"unspecified is never a valid from-state", commonv1.RecordState_RECORD_STATE_UNSPECIFIED, commonv1.RecordState_RECORD_STATE_NEW},
		{"self-loop not in the diagram", commonv1.RecordState_RECORD_STATE_SCORING, commonv1.RecordState_RECORD_STATE_SCORING},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if isAllowedTransition(tc.from, tc.to) {
				t.Errorf("%s -> %s accepted, want rejected", tc.from, tc.to)
			}
		})
	}
}
