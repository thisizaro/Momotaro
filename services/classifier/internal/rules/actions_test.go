package rules

import (
	"testing"

	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

// Every RootCauseBucket value must have an action rule and a rationale:
// iterating the generated enum's name map, not a hand-written list, means a
// bucket added to the proto without a rule here fails this test instead of
// silently defaulting to the zero value (SPEC.md section 7).
func TestActionForEveryBucket(t *testing.T) {
	for v, name := range commonv1.RootCauseBucket_name {
		bucket := commonv1.RootCauseBucket(v)
		rule := actionFor(bucket)
		if rule.Action == commonv1.ActionType_ACTION_TYPE_UNSPECIFIED {
			t.Errorf("bucket %s (%d): action left UNSPECIFIED", name, v)
		}
		if rule.Confidence < 0 || rule.Confidence > 1 {
			t.Errorf("bucket %s (%d): confidence %v outside [0,1]", name, v, rule.Confidence)
		}
		if composeRationale(bucket, rule.Action, "TEST_CODE", true, false) == "" {
			t.Errorf("bucket %s (%d): empty rationale", name, v)
		}
	}
}

// RISK_HOLD must always escalate rather than being auto-retried or
// auto-nudged: ARCHITECTURE.md section 5a, "never auto-retry around a risk
// decision." This is a compliance-relevant behaviour, not just a table row.
func TestRiskHoldAlwaysEscalates(t *testing.T) {
	rule := actionFor(commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_RISK_HOLD)
	if rule.Action != commonv1.ActionType_ACTION_TYPE_ESCALATE {
		t.Errorf("RISK_HOLD action = %v, want ESCALATE", rule.Action)
	}
}
