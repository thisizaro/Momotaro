package economics

import (
	"path/filepath"
	"runtime"
	"testing"

	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

// repoRoot walks up from this file so the tests do not depend on the working
// directory the runner happens to use.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "..")
}

func loadCheckedIn(t *testing.T) *Model {
	t.Helper()
	root := repoRoot(t)
	m, err := Load(filepath.Join(root, "configs", "intervention_costs.yaml"), filepath.Join(root, "configs", "recovery_priors.yaml"))
	if err != nil {
		t.Fatalf("Load the checked-in config: %v", err)
	}
	return m
}

// The checked-in config must actually parse and carry the values it claims.
// Without this the scorer could silently fall back to zeros and close every
// record as uneconomic.
func TestLoadReadsTheCheckedInConfig(t *testing.T) {
	m := loadCheckedIn(t)

	if got := m.costOf(commonv1.ActionType_ACTION_TYPE_RETRY); got.DirectPaise != 25 || got.IndirectPaise != 600 {
		t.Errorf("RETRY cost = %+v, want direct 25 and indirect 600 from configs/intervention_costs.yaml", got)
	}
	if got := m.liftBps(commonv1.ActionType_ACTION_TYPE_RETRY, commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK, 1); got != 3000 {
		t.Errorf("RETRY lift on TRANSIENT_BANK attempt 1 = %d bps, want 3000", got)
	}
	if got := m.liftBps(commonv1.ActionType_ACTION_TYPE_RETRY, commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK, 3); got != 500 {
		t.Errorf("RETRY lift on TRANSIENT_BANK attempt 3 = %d bps, want 500, probability must decay with attempt number", got)
	}
}

// A retry against an expired or blocked instrument cannot succeed no matter
// how many times it runs. The prior must be exactly zero, not merely small,
// or the scorer would keep buying attempts that have no path to working.
func TestRetryAgainstAHardDeclineHasZeroProbability(t *testing.T) {
	m := loadCheckedIn(t)

	for attempt := 1; attempt <= 4; attempt++ {
		if got := m.liftBps(commonv1.ActionType_ACTION_TYPE_RETRY, commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_HARD_DECLINE, attempt); got != 0 {
			t.Errorf("RETRY lift on HARD_DECLINE attempt %d = %d bps, want exactly 0", attempt, got)
		}
	}
	if got := m.liftBps(commonv1.ActionType_ACTION_TYPE_RETRY, commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_RISK_HOLD, 1); got != 0 {
		t.Errorf("RETRY lift on RISK_HOLD = %d bps, want exactly 0, a risk hold is never auto-retried", got)
	}
}

// Past the deepest modelled attempt the prior falls to the configured
// beyond-listed value, so the record closes rather than being chased on an
// extrapolated guess.
func TestLiftFallsToTheBeyondListedValuePastTheModelledAttempts(t *testing.T) {
	m := loadCheckedIn(t)

	if got := m.liftBps(commonv1.ActionType_ACTION_TYPE_RETRY, commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK, 99); got != m.beyondListedAttemptsBps {
		t.Errorf("RETRY lift at attempt 99 = %d bps, want the beyond-listed value %d", got, m.beyondListedAttemptsBps)
	}
}

// The formula from PRD.md section 2b, checked by hand:
// EV = lift * amount - direct_cost - indirect_cost.
func TestScoreComputesExpectedValueFromLiftAmountAndCosts(t *testing.T) {
	m := loadCheckedIn(t)
	const amountPaise = 100000 // 1000 rupees

	got := m.Score(commonv1.ActionType_ACTION_TYPE_RETRY, commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK, 1, amountPaise)

	// 3000 bps of 100000 paise is 30000, minus 25 direct and 600 indirect.
	const wantEV = 30000 - 25 - 600
	if got.EVPaise != wantEV {
		t.Errorf("EV = %v, want %v", got.EVPaise, wantEV)
	}
	if got.PRecovery != 0.30 {
		t.Errorf("PRecovery = %v, want 0.30 (3000 bps)", got.PRecovery)
	}
	if got.CostPaise != 625 {
		t.Errorf("CostPaise = %d, want 625 (25 direct + 600 indirect)", got.CostPaise)
	}
}

// The headline product claim: a small enough record is not worth chasing,
// because the intervention costs more than the recovery it buys.
//
// The threshold is LOW, and that is a real finding rather than a rounding
// detail. At the sourced Indian messaging rates a reminder costs 35 paise and
// buys 400 bps of lift on a transient failure, so it breaks even at about
// 8.75 rupees. One rupee is unambiguously below that.
func TestATinyRecordIsNotWorthChasing(t *testing.T) {
	m := loadCheckedIn(t)
	const amountPaise = 100 // 1 rupee

	_, ok := m.Best(spendingMenu(), commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK, amountPaise)
	if ok {
		t.Error("a 1 rupee record was judged worth chasing, want no positive-EV action so it closes as uneconomic")
	}
}

// The other side of the same coin, and the one that surprised me: a 10 rupee
// record IS worth a reminder, because 4 percent of 10 rupees beats a 35 paise
// SMS. Encoded so nobody "fixes" the low threshold later thinking it is a bug.
//
// The practical consequence for the demo is worth knowing: ClosedUneconomic
// will mostly be reached through zero-probability buckets (a retry against a
// hard decline can never work) rather than through small amounts, because
// Indian messaging is cheap enough that chasing is usually worth it.
func TestATenRupeeRecordIsStillWorthACheapNudge(t *testing.T) {
	m := loadCheckedIn(t)
	const amountPaise = 1000 // 10 rupees

	best, ok := m.Best(spendingMenu(), commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK, amountPaise)
	if !ok {
		t.Fatal("a 10 rupee record found no positive-EV action, want a reminder: 4 percent of 1000 paise beats its 35 paise cost")
	}
	if best.Action != commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER {
		t.Errorf("best action = %s, want NUDGE_REMINDER: a retry costs 625 paise and cannot pay for itself at this amount", best.Action)
	}
}

// Nothing can rescue a bucket whose probability is exactly zero, at any
// amount. This is the path ClosedUneconomic will actually be reached by.
func TestAZeroProbabilityBucketIsUneconomicAtAnyAmount(t *testing.T) {
	m := loadCheckedIn(t)

	// A retry against an expired instrument, on a large record. Only actions
	// that cannot work are permitted, so no amount makes them worth doing.
	permitted := []Candidate{{Action: commonv1.ActionType_ACTION_TYPE_RETRY, AttemptNo: 1}}
	if _, ok := m.Best(permitted, commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_HARD_DECLINE, 10000000); ok {
		t.Error("a retry against a hard decline was judged worth doing on a 100000 rupee record, want uneconomic: it cannot succeed at any value")
	}
}

func TestABigRecordIsWorthChasing(t *testing.T) {
	m := loadCheckedIn(t)
	const amountPaise = 500000 // 5000 rupees

	best, ok := m.Best(spendingMenu(), commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK, amountPaise)
	if !ok {
		t.Fatal("a 5000 rupee transient-bank failure found no positive-EV action, want a retry")
	}
	if best.Action != commonv1.ActionType_ACTION_TYPE_RETRY {
		t.Errorf("best action = %s, want RETRY for a transient bank failure", best.Action)
	}
}

// Guardrails run first and may only remove options. The scorer must never
// pick something they excluded, or a hard cap could be spent around.
func TestBestOnlyEverPicksFromThePermittedSet(t *testing.T) {
	m := loadCheckedIn(t)
	const amountPaise = 500000

	// Retry is the economically obvious choice here, so excluding it is the
	// sharpest test that the permitted set is respected.
	permitted := []Candidate{
		{Action: commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER, AttemptNo: 1},
		{Action: commonv1.ActionType_ACTION_TYPE_NUDGE_METHOD_UPDATE, AttemptNo: 1},
	}
	best, ok := m.Best(permitted, commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK, amountPaise)
	if ok && best.Action == commonv1.ActionType_ACTION_TYPE_RETRY {
		t.Error("Best returned RETRY when it was not in the permitted set")
	}
}

func TestBestReturnsNothingForAnEmptyPermittedSet(t *testing.T) {
	m := loadCheckedIn(t)

	if _, ok := m.Best(nil, commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK, 500000); ok {
		t.Error("Best found a positive-EV action in an empty permitted set")
	}
}

// Escalation costs a human's time and recovers nothing by itself, so it must
// never win on economics. It is a fallback the guardrails reach for, not a
// choice the scorer makes.
func TestEscalateNeverWinsOnExpectedValue(t *testing.T) {
	m := loadCheckedIn(t)

	menu := append(spendingMenu(), Candidate{Action: commonv1.ActionType_ACTION_TYPE_ESCALATE, AttemptNo: 1})
	best, ok := m.Best(menu, commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK, 500000)
	if ok && best.Action == commonv1.ActionType_ACTION_TYPE_ESCALATE {
		t.Error("ESCALATE won on expected value, want it to lose to any real intervention")
	}
}

// spendingMenu is every money-costing action on its first attempt, the shape
// the guardrails hand the scorer for a fresh record.
func spendingMenu() []Candidate {
	return []Candidate{
		{Action: commonv1.ActionType_ACTION_TYPE_RETRY, AttemptNo: 1},
		{Action: commonv1.ActionType_ACTION_TYPE_NUDGE_METHOD_UPDATE, AttemptNo: 1},
		{Action: commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER, AttemptNo: 1},
	}
}

// An action that exactly breaks even must not be chosen. NONE is the natural
// case: zero lift and zero cost gives an EV of exactly zero, and doing nothing
// is not an intervention worth scheduling. Without this, a `< 0` comparison
// would look correct against every other test in this file.
func TestAnExactlyBreakEvenActionIsNotWorthDoing(t *testing.T) {
	m := loadCheckedIn(t)

	if got := m.Score(commonv1.ActionType_ACTION_TYPE_NONE, commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK, 1, 500000); got.EVPaise != 0 {
		t.Fatalf("NONE scored %v, this test needs it to be exactly 0 to be meaningful", got.EVPaise)
	}
	permitted := []Candidate{{Action: commonv1.ActionType_ACTION_TYPE_NONE, AttemptNo: 1}}
	if _, ok := m.Best(permitted, commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK, 500000); ok {
		t.Error("an action with exactly zero expected value was judged worth doing, want strictly positive only")
	}
}

// all_attempts pins a combination to one value for every attempt number. The
// checked-in config only ever pins to zero, and the beyond-listed fallback is
// also zero today, so the real config cannot tell the two paths apart. This
// builds a model where they differ, so the pin is actually proven to work
// rather than coincidentally agreeing with the fallback.
func TestAllAttemptsPinsEveryAttemptEvenWhenTheFallbackDiffers(t *testing.T) {
	pinned := 0
	m := &Model{
		costs: map[commonv1.ActionType]actionCost{
			commonv1.ActionType_ACTION_TYPE_RETRY: {DirectPaise: 25, IndirectPaise: 600},
		},
		priors: map[commonv1.ActionType]map[commonv1.RootCauseBucket]attemptPriors{
			commonv1.ActionType_ACTION_TYPE_RETRY: {
				commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_HARD_DECLINE: {AllAttempts: &pinned},
			},
		},
		// Deliberately non-zero and different from the pin, so falling through
		// to it is visible instead of masquerading as the pinned value.
		beyondListedAttemptsBps: 9999,
	}

	for attempt := 1; attempt <= 5; attempt++ {
		if got := m.liftBps(commonv1.ActionType_ACTION_TYPE_RETRY, commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_HARD_DECLINE, attempt); got != 0 {
			t.Errorf("attempt %d: lift = %d bps, want the pinned 0; the all_attempts pin fell through to the beyond-listed fallback", attempt, got)
		}
	}
}
