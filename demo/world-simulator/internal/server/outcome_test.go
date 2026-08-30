package server

import (
	"testing"

	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

// fixedRand always returns v, so a roll's outcome is a pure function of
// the boundary being tested rather than luck.
type fixedRand float64

func (f fixedRand) Float64() float64 { return float64(f) }

func TestRollOutcomeUsesRecoveryProbabilityForTheCorrectAction(t *testing.T) {
	profile := groundTruthProfile{
		TrueBucket:             commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK,
		RecoveryProbability:    0.80,
		WrongActionProbability: 0.05,
	}
	if !rollOutcome(fixedRand(0.79), commonv1.ActionType_ACTION_TYPE_RETRY, profile) {
		t.Error("roll 0.79 against RecoveryProbability 0.80: want success")
	}
	if rollOutcome(fixedRand(0.81), commonv1.ActionType_ACTION_TYPE_RETRY, profile) {
		t.Error("roll 0.81 against RecoveryProbability 0.80: want failure")
	}
}

func TestRollOutcomeUsesWrongActionProbabilityForAnIncorrectAction(t *testing.T) {
	profile := groundTruthProfile{
		TrueBucket:             commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_HARD_DECLINE,
		RecoveryProbability:    0.80,
		WrongActionProbability: 0.05,
	}
	// HARD_DECLINE's correct action is NUDGE_METHOD_UPDATE (bucket.go);
	// RETRY here is the wrong action, so WrongActionProbability applies,
	// not RecoveryProbability, even though the roll would succeed against
	// the latter.
	if rollOutcome(fixedRand(0.10), commonv1.ActionType_ACTION_TYPE_RETRY, profile) {
		t.Error("roll 0.10 against WrongActionProbability 0.05 for the wrong action: want failure")
	}
	if !rollOutcome(fixedRand(0.01), commonv1.ActionType_ACTION_TYPE_RETRY, profile) {
		t.Error("roll 0.01 against WrongActionProbability 0.05 for the wrong action: want success")
	}
}

func TestRollOutcomeUnrecognisedBucketIsNeverCorrect(t *testing.T) {
	profile := groundTruthProfile{
		TrueBucket:             commonv1.RootCauseBucket(999),
		RecoveryProbability:    0.99,
		WrongActionProbability: 0.01,
	}
	if rollOutcome(fixedRand(0.02), commonv1.ActionType_ACTION_TYPE_RETRY, profile) {
		t.Error("unrecognised true bucket: want WrongActionProbability applied (roll 0.02 vs 0.01), not RecoveryProbability")
	}
}

func TestIsCorrectActionMatchesTheMirrorTable(t *testing.T) {
	cases := []struct {
		bucket commonv1.RootCauseBucket
		action commonv1.ActionType
		want   bool
	}{
		{commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK, commonv1.ActionType_ACTION_TYPE_RETRY, true},
		{commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK, commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER, false},
		{commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_HARD_DECLINE, commonv1.ActionType_ACTION_TYPE_NUDGE_METHOD_UPDATE, true},
		{commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_RISK_HOLD, commonv1.ActionType_ACTION_TYPE_ESCALATE, true},
		{commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_ABANDONMENT, commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER, true},
		{commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_OVERDUE, commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER, true},
		{commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_USER_ACTION_NEEDED, commonv1.ActionType_ACTION_TYPE_NUDGE_METHOD_UPDATE, true},
	}
	for _, c := range cases {
		if got := isCorrectAction(c.action, c.bucket); got != c.want {
			t.Errorf("isCorrectAction(%v, %v) = %v, want %v", c.action, c.bucket, got, c.want)
		}
	}
}

func TestIsNudge(t *testing.T) {
	if !isNudge(commonv1.ActionType_ACTION_TYPE_NUDGE_METHOD_UPDATE) {
		t.Error("NUDGE_METHOD_UPDATE: want isNudge true")
	}
	if !isNudge(commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER) {
		t.Error("NUDGE_REMINDER: want isNudge true")
	}
	if isNudge(commonv1.ActionType_ACTION_TYPE_RETRY) {
		t.Error("RETRY: want isNudge false")
	}
	if isNudge(commonv1.ActionType_ACTION_TYPE_ESCALATE) {
		t.Error("ESCALATE: want isNudge false")
	}
}
