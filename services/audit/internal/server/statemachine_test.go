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
		{commonv1.RecordState_RECORD_STATE_SCORING, commonv1.RecordState_RECORD_STATE_ESCALATED},
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
		{"cannot skip scoring to recovered", commonv1.RecordState_RECORD_STATE_NEW, commonv1.RecordState_RECORD_STATE_RECOVERED},
		{"cannot skip scoring to retry scheduled", commonv1.RecordState_RECORD_STATE_NEW, commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED},
		{"cannot skip scoring to nudge scheduled", commonv1.RecordState_RECORD_STATE_NEW, commonv1.RecordState_RECORD_STATE_NUDGE_SCHEDULED},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if isAllowedTransition(tc.from, tc.to) {
				t.Errorf("%s -> %s accepted, want rejected", tc.from, tc.to)
			}
		})
	}
}

// A sent nudge stays in Nudged: the scheduler claims it into that state and
// the send's PENDING outcome maps to it too, so the trail has a self-edge
// carrying the attempt and its cost. Found by the Phase 1 smoke test, the only
// thing that walks the nudge path end to end.
func TestIsAllowedTransitionAcceptsTheSentNudgeSelfEdge(t *testing.T) {
	if !isAllowedTransition(commonv1.RecordState_RECORD_STATE_NUDGED, commonv1.RecordState_RECORD_STATE_NUDGED) {
		t.Error("Nudged -> Nudged rejected, but that is what executing a nudge actually records")
	}
}

// Guards the reasoning above: the self-edge is specific to Nudged, where the
// claim and the outcome genuinely share a state. A retry never does, so a
// Retrying self-edge would mean something had gone wrong.
func TestIsAllowedTransitionStillRejectsOtherSelfEdges(t *testing.T) {
	for _, s := range []commonv1.RecordState{
		commonv1.RecordState_RECORD_STATE_RETRYING,
		commonv1.RecordState_RECORD_STATE_SCORING,
		commonv1.RecordState_RECORD_STATE_RECOVERED,
		commonv1.RecordState_RECORD_STATE_ESCALATED,
	} {
		if isAllowedTransition(s, s) {
			t.Errorf("%v -> %v accepted, want rejected", s, s)
		}
	}
}
