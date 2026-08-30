package server

import (
	"math"
	"testing"

	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

// approxEqual tolerates ordinary float64 arithmetic error, not a
// meaningful modelling difference.
func approxEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}

func TestEvaluateNaivePolicyRetryCorrectBucketUsesRecoveryProbability(t *testing.T) {
	// TRANSIENT_BANK's correct action is RETRY, so all three retries see
	// p=0.8; the naive nudge (a generic reminder) is not TRANSIENT_BANK's
	// correct action, so if reached it would see wrong=0.05. Hand-computed:
	// qRetry=0.2, expectedRetryAttempts=1+0.2+0.04=1.24,
	// probAllRetriesFail=0.2^3=0.008, probRecovered=1-0.008*0.95=0.9924,
	// expectedSpend=1.24*25+0.008*25=31.2.
	got := evaluateNaivePolicy(commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK, 0.8, 0.05, 100000)

	wantRecovered := 0.9924 * 100000
	wantSpend := 31.2
	if !approxEqual(got.ExpectedRecoveredPaise, wantRecovered) {
		t.Errorf("ExpectedRecoveredPaise = %v, want %v", got.ExpectedRecoveredPaise, wantRecovered)
	}
	if !approxEqual(got.ExpectedSpendPaise, wantSpend) {
		t.Errorf("ExpectedSpendPaise = %v, want %v", got.ExpectedSpendPaise, wantSpend)
	}
}

func TestEvaluateNaivePolicyRiskHoldSpendsForNearZeroRecovery(t *testing.T) {
	// RISK_HOLD's correct action is ESCALATE: neither RETRY nor the naive
	// nudge is correct, so every one of the four attempts sees
	// wrong=0.0. All four attempts still happen and still cost money --
	// this is Unit K's own prediction (docs/PHASE5_IMPLEMENTATION.md):
	// "it pays to chase RISK_HOLD records whose priors are zero for a
	// reason". qRetry=1.0, expectedRetryAttempts=3,
	// probAllRetriesFail=1.0, probRecovered=1-1.0*(1-0.0)=0,
	// expectedSpend=3*25+1.0*25=100.
	got := evaluateNaivePolicy(commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_RISK_HOLD, 0.05, 0.0, 100000)

	if got.ExpectedRecoveredPaise != 0 {
		t.Errorf("ExpectedRecoveredPaise = %v, want 0", got.ExpectedRecoveredPaise)
	}
	if !approxEqual(got.ExpectedSpendPaise, 100) {
		t.Errorf("ExpectedSpendPaise = %v, want 100 (3 retries + 1 nudge, all charged regardless of outcome)", got.ExpectedSpendPaise)
	}
}

func TestEvaluateNaivePolicyNudgeCorrectBucketUsesRecoveryProbabilityOnNudge(t *testing.T) {
	// ABANDONMENT's correct action is NUDGE_REMINDER, exactly the naive
	// policy's own nudge choice, so the nudge step (reached only if all
	// three retries fail) sees p=0.25, not wrong=0.05. Retry is still the
	// wrong action here, so all three retries see wrong=0.05.
	// qRetry=0.95, expectedRetryAttempts=1+0.95+0.9025=2.8525,
	// probAllRetriesFail=0.95^3=0.857375,
	// probRecovered=1-0.857375*(1-0.25)=1-0.857375*0.75=0.35696875,
	// expectedSpend=2.8525*25+0.857375*25=92.746875.
	got := evaluateNaivePolicy(commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_ABANDONMENT, 0.25, 0.05, 100000)

	wantRecovered := 0.35696875 * 100000
	wantSpend := 92.746875
	if !approxEqual(got.ExpectedRecoveredPaise, wantRecovered) {
		t.Errorf("ExpectedRecoveredPaise = %v, want %v", got.ExpectedRecoveredPaise, wantRecovered)
	}
	if !approxEqual(got.ExpectedSpendPaise, wantSpend) {
		t.Errorf("ExpectedSpendPaise = %v, want %v", got.ExpectedSpendPaise, wantSpend)
	}
}

func TestEvaluateNaivePolicyZeroAmountRecoversNothingButStillSpends(t *testing.T) {
	got := evaluateNaivePolicy(commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK, 0.8, 0.05, 0)
	if got.ExpectedRecoveredPaise != 0 {
		t.Errorf("ExpectedRecoveredPaise = %v, want 0 for a zero-amount record", got.ExpectedRecoveredPaise)
	}
	if got.ExpectedSpendPaise <= 0 {
		t.Errorf("ExpectedSpendPaise = %v, want > 0: attempts still cost money regardless of amount", got.ExpectedSpendPaise)
	}
}

func TestBaselineComparisonNilWithoutRows(t *testing.T) {
	if got := baselineComparison(nil); got != nil {
		t.Errorf("baselineComparison(nil) = %+v, want nil", got)
	}
	if got := baselineComparison([]groundTruthRow{}); got != nil {
		t.Errorf("baselineComparison(empty) = %+v, want nil", got)
	}
}

func TestBaselineComparisonSumsAndRoundsOnceAcrossRows(t *testing.T) {
	rows := []groundTruthRow{
		{TrueBucket: commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK, RecoveryProbability: 0.8, WrongActionProbability: 0.05, AmountPaise: 100000},
		{TrueBucket: commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_RISK_HOLD, RecoveryProbability: 0.05, WrongActionProbability: 0.0, AmountPaise: 100000},
	}
	got := baselineComparison(rows)
	if got == nil {
		t.Fatal("baselineComparison(rows) = nil, want populated")
	}
	if got.PolicyName != naivePolicyName {
		t.Errorf("PolicyName = %q, want %q", got.PolicyName, naivePolicyName)
	}
	// 99240 (TRANSIENT_BANK) + 0 (RISK_HOLD) = 99240.
	if got.GrossRecoveredPaise != 99240 {
		t.Errorf("GrossRecoveredPaise = %d, want 99240", got.GrossRecoveredPaise)
	}
	// round(31.2) + round-summed(100) = 31 + 100 = 131, computed as one
	// sum (31.2+100=131.2) rounded once, not two independently rounded
	// numbers added.
	if got.InterventionSpendPaise != 131 {
		t.Errorf("InterventionSpendPaise = %d, want 131", got.InterventionSpendPaise)
	}
	if got.NetRecoveredPaise != got.GrossRecoveredPaise-got.InterventionSpendPaise {
		t.Errorf("NetRecoveredPaise = %d, want GrossRecoveredPaise - InterventionSpendPaise", got.NetRecoveredPaise)
	}
	if got.Note == "" {
		t.Error("Note is empty, want the honesty caveat")
	}
}

// TestCorrectActionForCoversEveryBucket guards against a bucket added to
// the proto without a corresponding entry here: an unrecognised bucket
// silently falls back to "no action is correct" (the zero value of a
// missing map lookup never equals any ActionType), which would make every
// naive attempt look wrong for that bucket without anyone deciding that on
// purpose.
func TestCorrectActionForCoversEveryBucket(t *testing.T) {
	for name, val := range commonv1.RootCauseBucket_value {
		bucket := commonv1.RootCauseBucket(val)
		if _, ok := correctActionFor[bucket]; !ok {
			t.Errorf("correctActionFor has no entry for %s (%d)", name, val)
		}
	}
}
