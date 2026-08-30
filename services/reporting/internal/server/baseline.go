// Phase 5 Unit K: what a naive, un-diagnosed recovery policy would have
// recovered on the same sealed GROUND_TRUTH the real batch ran against, so
// "measured money recovered" is a result rather than a number standing
// alone (docs/PHASE5_IMPLEMENTATION.md Unit K).
//
// Computed analytically -- expected value, not a second run of the batch.
// GROUND_TRUTH already carries everything the computation needs
// (recovery_probability, wrong_action_probability, true_bucket), and
// Reporting is already one of only two services permitted to read it.
//
// The fixed policy: retry every record up to naiveRetryAttempts times,
// stopping as soon as one succeeds (a real competing system still knows
// when a payment cleared; that is not "economics", it is not
// re-presenting money already collected), then, if every retry failed,
// send exactly one generic reminder nudge. No bucket-aware action choice,
// no expected-value gating, no guardrail beyond the attempt cap itself --
// it acts on every record, including the ones a diagnosed system would
// recognise up front as not worth chasing at all. That last part is
// deliberate: docs/PHASE5_IMPLEMENTATION.md predicts this is exactly what
// makes the comparison interesting, since it pays to chase HARD_DECLINE
// and RISK_HOLD records whose priors are near zero for a reason.
//
// HONESTY REQUIREMENT (docs/PHASE5_IMPLEMENTATION.md Unit K, non-negotiable):
// both this policy's numbers and the real batch's are evaluated in a world
// we authored. naivePolicyNote carries that caveat on the wire so a
// dashboard tile cannot drop it in transit.
package server

import (
	"math"

	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	reportingv1 "github.com/thisizaro/Momotaro/proto/gen/reporting/v1"
)

const (
	naiveRetryAttempts = 3

	// naiveNudgeAction is the naive policy's one nudge: a generic
	// reminder, not a targeted "update your payment method" ask, since
	// that specificity is exactly the diagnosis this policy deliberately
	// lacks. Correct (GROUND_TRUTH.recovery_probability applies) only for
	// the two buckets whose real correct action already is a reminder
	// (ABANDONMENT, OVERDUE); wrong_action_probability applies everywhere
	// else, including HARD_DECLINE, where the real system's fix is
	// NUDGE_METHOD_UPDATE instead.
	naiveNudgeAction = commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER

	// naiveRetryCostPaise mirrors services/executor/internal/ports/
	// cost.go's retryCostPaise. That package is private to
	// services/executor and a cross-service import is a compile error
	// (docs/AGENTS.md "Repo structure"), so this is a third checked-in
	// copy of the same number, the same duplication precedent as
	// demo/world-simulator/internal/server/bucket.go's correctActionFor
	// below. Guarded against drift by
	// TestReportingCostsMatchInterventionCostsYAML.
	naiveRetryCostPaise = 25
	// naiveNudgeCostPaise is the naive policy's nudge channel cost: SMS,
	// the same "always available, cheapest" default
	// services/executor/internal/ports/cost.go's channelCostPaise falls
	// back to for an unspecified channel. A naive/generic system has no
	// reason to prefer WhatsApp.
	naiveNudgeCostPaise = 25

	// naivePolicyName is BatchReport.baseline_comparison.policy_name's
	// wire value, docs/API_GATEWAY.md's own example.
	naivePolicyName = "naive_retry3_nudge1"

	// naivePolicyNote is the honesty caveat this project holds itself to
	// for every modelled number (configs/*.yaml's provenance tags),
	// carried on the wire itself.
	naivePolicyNote = "Evaluated analytically against the same sealed ground truth using a fixed naive policy (retry every record up to 3x, nudge every record once, no economics). Measures this policy against our modelled world, not real money."
)

// correctActionFor mirrors services/classifier/internal/rules/actions.go's
// bucket -> recommended-action table and
// demo/world-simulator/internal/server/bucket.go's identically-named,
// identically-justified copy: "what a diagnosed system would have done for
// this true bucket", which is what distinguishes a record where
// GROUND_TRUTH.recovery_probability applies from one where
// wrong_action_probability does. Must stay in sync with both; same
// precedent, same reason (cross-service import is a compile error).
var correctActionFor = map[commonv1.RootCauseBucket]commonv1.ActionType{
	commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK:     commonv1.ActionType_ACTION_TYPE_RETRY,
	commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_INSUFFICIENT_FUNDS: commonv1.ActionType_ACTION_TYPE_RETRY,
	commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_HARD_DECLINE:       commonv1.ActionType_ACTION_TYPE_NUDGE_METHOD_UPDATE,
	commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_USER_ACTION_NEEDED: commonv1.ActionType_ACTION_TYPE_NUDGE_METHOD_UPDATE,
	commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_RISK_HOLD:          commonv1.ActionType_ACTION_TYPE_ESCALATE,
	commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_ABANDONMENT:        commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER,
	commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_OVERDUE:            commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER,
	commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_UNSPECIFIED:        commonv1.ActionType_ACTION_TYPE_ESCALATE,
}

// naiveRecordOutcome is the naive policy's expected result for one record,
// in expectation rather than by dice roll: this is an analytical model, so
// "probability of recovery" IS the number, not something to sample.
type naiveRecordOutcome struct {
	ExpectedRecoveredPaise float64
	ExpectedSpendPaise     float64
}

// evaluateNaivePolicy computes the naive policy's expected result for one
// record. p is GROUND_TRUTH.recovery_probability (given the CORRECT action
// for trueBucket), wrong is wrong_action_probability (given any other
// action); both are already constrained to [0,1] by the database CHECK
// constraints that produced them (migrations/00001_initial_schema.sql).
//
// Model: naiveRetryAttempts independent Bernoulli(pRetry) trials, stopping
// at the first success -- matching World Simulator's own "each call
// re-rolls independently, nothing remembers a prior attempt's result"
// semantics (demo/world-simulator/internal/server/outcome.go), just
// computed in expectation rather than rolled. If every retry fails, one
// more Bernoulli(pNudge) trial. Every attempt, successful or not, costs
// its fixed direct cost: the retry fee is charged win or lose
// (services/executor/internal/ports/cost.go), and so is the SMS.
func evaluateNaivePolicy(trueBucket commonv1.RootCauseBucket, p, wrong float64, amountPaise int64) naiveRecordOutcome {
	pRetry := wrong
	if correctActionFor[trueBucket] == commonv1.ActionType_ACTION_TYPE_RETRY {
		pRetry = p
	}
	pNudge := wrong
	if correctActionFor[trueBucket] == naiveNudgeAction {
		pNudge = p
	}

	qRetry := 1 - pRetry
	expectedRetryAttempts := 0.0
	qPow := 1.0
	for i := 0; i < naiveRetryAttempts; i++ {
		expectedRetryAttempts += qPow
		qPow *= qRetry
	}
	probAllRetriesFail := qPow // qRetry^naiveRetryAttempts

	probRecovered := 1 - probAllRetriesFail*(1-pNudge)
	expectedSpend := expectedRetryAttempts*naiveRetryCostPaise + probAllRetriesFail*naiveNudgeCostPaise

	return naiveRecordOutcome{
		ExpectedRecoveredPaise: probRecovered * float64(amountPaise),
		ExpectedSpendPaise:     expectedSpend,
	}
}

// baselineComparison evaluates the naive policy over every row and rounds
// once at the end, not per record, so per-record fractional paise do not
// compound into visible drift across a large batch. Returns nil for an
// empty rows: the caller (GetBatchReport) treats that as "no ground truth
// for this batch", the same absent-not-zeroed rule accuracy already
// follows (docs/API_GATEWAY.md: "a missing key means no answer key
// exists, distinct from a real zero").
func baselineComparison(rows []groundTruthRow) *reportingv1.BaselineComparison {
	if len(rows) == 0 {
		return nil
	}

	var gross, spend float64
	for _, r := range rows {
		o := evaluateNaivePolicy(r.TrueBucket, r.RecoveryProbability, r.WrongActionProbability, r.AmountPaise)
		gross += o.ExpectedRecoveredPaise
		spend += o.ExpectedSpendPaise
	}

	grossPaise := int64(math.Round(gross))
	spendPaise := int64(math.Round(spend))
	return &reportingv1.BaselineComparison{
		PolicyName:             naivePolicyName,
		GrossRecoveredPaise:    grossPaise,
		InterventionSpendPaise: spendPaise,
		NetRecoveredPaise:      grossPaise - spendPaise,
		Note:                   naivePolicyNote,
	}
}
