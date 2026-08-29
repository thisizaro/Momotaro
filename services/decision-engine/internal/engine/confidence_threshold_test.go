package engine

import (
	"path/filepath"
	"runtime"
	"testing"
	"time"

	classifierv1 "github.com/thisizaro/Momotaro/proto/gen/classifier/v1"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	"github.com/thisizaro/Momotaro/services/decision-engine/internal/economics"
)

// Phase 3 Unit G: Engine.decide has no I/O (it only reaches scoreAndRoute,
// which is itself pure), so these run as plain unit tests against a
// hand-built Engine, in the default (untagged) tier, rather than the
// integration-tagged HandleMessage path. testEconomics (testhelpers_test.go)
// loads the same checked-in config with no Postgres or Kafka involved
// either, but that file is gated behind the integration tag as a matter of
// organisation, not because the helper itself needs a database; this is a
// second, untagged copy for exactly that reason.
func testEngineWithThreshold(t *testing.T, threshold float64) *Engine {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Join(filepath.Dir(file), "..", "..", "..", "..")
	model, err := economics.Load(filepath.Join(root, "configs", "intervention_costs.yaml"), filepath.Join(root, "configs", "recovery_priors.yaml"))
	if err != nil {
		t.Fatalf("load economics config: %v", err)
	}
	return &Engine{
		economics: model,
		cfg:       Config{Guardrails: testGuardrails, ClassifyConfidenceThreshold: threshold},
	}
}

func TestDecideEscalatesJustBelowConfidenceThreshold(t *testing.T) {
	e := testEngineWithThreshold(t, 0.5)
	resp := &classifierv1.ClassifyResponse{
		Bucket:            commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK,
		RecommendedAction: commonv1.ActionType_ACTION_TYPE_RETRY,
		Confidence:        0.49,
	}

	steps, action, score, _ := e.decide(resp, freshHistory(), 10000, time.Now())

	if len(steps) != 1 || steps[0].From != commonv1.RecordState_RECORD_STATE_NEW || steps[0].To != commonv1.RecordState_RECORD_STATE_ESCALATED {
		t.Fatalf("steps = %+v, want a single New -> Escalated transition", steps)
	}
	if steps[0].Reason != "classification confidence below threshold" {
		t.Errorf("reason = %q, want %q", steps[0].Reason, "classification confidence below threshold")
	}
	if action != commonv1.ActionType_ACTION_TYPE_UNSPECIFIED {
		t.Errorf("pending action = %v, want UNSPECIFIED: escalation bypasses economics entirely", action)
	}
	if score != (economics.Score{}) {
		t.Errorf("score = %+v, want the zero value: the economics gate was never reached", score)
	}
}

// The comparison is strictly <. Confidence exactly at the threshold must
// fall through to economics, or the threshold would silently behave as if
// it were one ULP higher than configured.
func TestDecideDoesNotEscalateExactlyAtConfidenceThreshold(t *testing.T) {
	e := testEngineWithThreshold(t, 0.5)
	resp := &classifierv1.ClassifyResponse{
		Bucket:            commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK,
		RecommendedAction: commonv1.ActionType_ACTION_TYPE_RETRY,
		Confidence:        0.5,
	}

	steps, _, _, _ := e.decide(resp, freshHistory(), 10000, time.Now())

	last := steps[len(steps)-1]
	if last.Reason == "classification confidence below threshold" {
		t.Errorf("reason = %q at confidence == threshold, want it to fall through to economics (the comparison must be strict <)", last.Reason)
	}
}

func TestDecideDoesNotEscalateJustAboveConfidenceThreshold(t *testing.T) {
	e := testEngineWithThreshold(t, 0.5)
	resp := &classifierv1.ClassifyResponse{
		Bucket:            commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK,
		RecommendedAction: commonv1.ActionType_ACTION_TYPE_RETRY,
		Confidence:        0.51,
	}

	steps, _, _, _ := e.decide(resp, freshHistory(), 10000, time.Now())

	last := steps[len(steps)-1]
	if last.Reason == "classification confidence below threshold" {
		t.Errorf("reason = %q just above threshold, want it to fall through to economics", last.Reason)
	}
}

// The default threshold, 0.0, must provably escalate nothing on confidence:
// every rules-engine confidence value is > 0, and this is what makes G safe
// to merge without touching any of the six e2e tests.
func TestDecideEscalatesNothingOnConfidenceAtDefaultThreshold(t *testing.T) {
	e := testEngineWithThreshold(t, 0.0)
	for _, confidence := range []float64{0.0, 0.01, 0.5, 1.0} {
		resp := &classifierv1.ClassifyResponse{
			Bucket:            commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK,
			RecommendedAction: commonv1.ActionType_ACTION_TYPE_RETRY,
			Confidence:        confidence,
		}
		steps, _, _, _ := e.decide(resp, freshHistory(), 10000, time.Now())
		last := steps[len(steps)-1]
		if last.Reason == "classification confidence below threshold" {
			t.Errorf("confidence=%v: reason = %q at the default threshold 0.0, want no confidence escalation ever", confidence, last.Reason)
		}
	}
}

// The interaction the LLD calls out by name: the rules engine's
// unknown-code path always returns confidence 0.0 AND recommends ESCALATE
// (rules/actions.go's ROOT_CAUSE_BUCKET_UNSPECIFIED row), so with any
// threshold above 0.0 both the new confidence check and the existing
// escalate-recommendation check are satisfied by the same record. The audit
// trail must still say "classifier recommended escalation", not
// "classification confidence below threshold", so it can tell "we do not
// recognise this failure code" from "the model was unsure".
func TestDecideNamesEscalationReasonNotConfidenceForUnknownCodePath(t *testing.T) {
	e := testEngineWithThreshold(t, 0.5)
	resp := &classifierv1.ClassifyResponse{
		Bucket:            commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_UNSPECIFIED,
		RecommendedAction: commonv1.ActionType_ACTION_TYPE_ESCALATE,
		Confidence:        0.0,
	}

	steps, _, _, _ := e.decide(resp, freshHistory(), 10000, time.Now())

	last := steps[len(steps)-1]
	if last.Reason != "classifier recommended escalation" {
		t.Errorf("reason = %q, want %q: this is the unknown-code path, not a genuinely low-confidence model answer", last.Reason, "classifier recommended escalation")
	}
}

// A genuinely low-confidence model answer that does NOT recommend ESCALATE
// is the case the threshold actually exists to catch: confirms the two
// reasons are still distinguishable in the other direction.
func TestDecideNamesConfidenceReasonForLowConfidenceNonEscalateRecommendation(t *testing.T) {
	e := testEngineWithThreshold(t, 0.5)
	resp := &classifierv1.ClassifyResponse{
		Bucket:            commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK,
		RecommendedAction: commonv1.ActionType_ACTION_TYPE_RETRY,
		Confidence:        0.3,
	}

	steps, _, _, _ := e.decide(resp, freshHistory(), 10000, time.Now())

	last := steps[len(steps)-1]
	if last.Reason != "classification confidence below threshold" {
		t.Errorf("reason = %q, want %q", last.Reason, "classification confidence below threshold")
	}
}
