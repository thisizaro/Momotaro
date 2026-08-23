package engine

import (
	"testing"

	classifierv1 "github.com/thisizaro/Momotaro/proto/gen/classifier/v1"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

func TestDecideAfterClassify(t *testing.T) {
	tests := []struct {
		name              string
		action            commonv1.ActionType
		wantState         commonv1.RecordState
		wantPendingAction commonv1.ActionType
	}{
		{
			name:              "retry is scheduled",
			action:            commonv1.ActionType_ACTION_TYPE_RETRY,
			wantState:         commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED,
			wantPendingAction: commonv1.ActionType_ACTION_TYPE_RETRY,
		},
		{
			name:              "nudge method-update is scheduled",
			action:            commonv1.ActionType_ACTION_TYPE_NUDGE_METHOD_UPDATE,
			wantState:         commonv1.RecordState_RECORD_STATE_NUDGE_SCHEDULED,
			wantPendingAction: commonv1.ActionType_ACTION_TYPE_NUDGE_METHOD_UPDATE,
		},
		{
			name:              "nudge reminder is scheduled",
			action:            commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER,
			wantState:         commonv1.RecordState_RECORD_STATE_NUDGE_SCHEDULED,
			wantPendingAction: commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER,
		},
		{
			name:              "explicit escalate goes straight to escalated",
			action:            commonv1.ActionType_ACTION_TYPE_ESCALATE,
			wantState:         commonv1.RecordState_RECORD_STATE_ESCALATED,
			wantPendingAction: commonv1.ActionType_ACTION_TYPE_UNSPECIFIED,
		},
		{
			name:              "none has no Phase 1 economics gate to land in, so it escalates",
			action:            commonv1.ActionType_ACTION_TYPE_NONE,
			wantState:         commonv1.RecordState_RECORD_STATE_ESCALATED,
			wantPendingAction: commonv1.ActionType_ACTION_TYPE_UNSPECIFIED,
		},
		{
			name:              "unspecified action escalates rather than being silently dropped",
			action:            commonv1.ActionType_ACTION_TYPE_UNSPECIFIED,
			wantState:         commonv1.RecordState_RECORD_STATE_ESCALATED,
			wantPendingAction: commonv1.ActionType_ACTION_TYPE_UNSPECIFIED,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := &classifierv1.ClassifyResponse{RecommendedAction: tt.action}
			state, pendingAction, reason := decideAfterClassify(resp)
			if state != tt.wantState {
				t.Errorf("state = %v, want %v", state, tt.wantState)
			}
			if pendingAction != tt.wantPendingAction {
				t.Errorf("pendingAction = %v, want %v", pendingAction, tt.wantPendingAction)
			}
			if reason == "" {
				t.Error("reason must not be empty, it lands in the audit trail verbatim")
			}
		})
	}
}

func TestDecideAfterExecute(t *testing.T) {
	tests := []struct {
		name      string
		pending   commonv1.ActionType
		outcome   commonv1.Outcome
		wantState commonv1.RecordState
	}{
		{
			name:      "success recovers regardless of action type",
			pending:   commonv1.ActionType_ACTION_TYPE_RETRY,
			outcome:   commonv1.Outcome_OUTCOME_SUCCESS,
			wantState: commonv1.RecordState_RECORD_STATE_RECOVERED,
		},
		{
			name:      "failed retry escalates, Phase 1 has no retry budget to fall back into",
			pending:   commonv1.ActionType_ACTION_TYPE_RETRY,
			outcome:   commonv1.Outcome_OUTCOME_FAILURE,
			wantState: commonv1.RecordState_RECORD_STATE_ESCALATED,
		},
		{
			name:      "failed nudge escalates",
			pending:   commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER,
			outcome:   commonv1.Outcome_OUTCOME_FAILURE,
			wantState: commonv1.RecordState_RECORD_STATE_ESCALATED,
		},
		{
			name:      "pending nudge parks in Nudged awaiting the delayed callback",
			pending:   commonv1.ActionType_ACTION_TYPE_NUDGE_METHOD_UPDATE,
			outcome:   commonv1.Outcome_OUTCOME_PENDING,
			wantState: commonv1.RecordState_RECORD_STATE_NUDGED,
		},
		{
			name:      "pending retry is unexpected (retries are synchronous) and escalates",
			pending:   commonv1.ActionType_ACTION_TYPE_RETRY,
			outcome:   commonv1.Outcome_OUTCOME_PENDING,
			wantState: commonv1.RecordState_RECORD_STATE_ESCALATED,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state, reason := decideAfterExecute(tt.pending, tt.outcome)
			if state != tt.wantState {
				t.Errorf("state = %v, want %v", state, tt.wantState)
			}
			if reason == "" {
				t.Error("reason must not be empty, it lands in the audit trail verbatim")
			}
		})
	}
}
