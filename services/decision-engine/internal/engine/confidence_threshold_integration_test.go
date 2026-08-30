//go:build integration

// Phase 3 Unit G: the confidence-threshold escalation must be visible in the
// audit trail with the RIGHT reason, not just as an ESCALATED state that
// could have come from anywhere. These run through the real HandleMessage
// path against Postgres because that is the only way to prove what actually
// lands in audit_entry.reason, not what decide's return value claims it
// will be.
package engine

import (
	"context"
	"testing"

	"github.com/thisizaro/Momotaro/internal/platform/clock"
	classifierv1 "github.com/thisizaro/Momotaro/proto/gen/classifier/v1"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

func TestHandleMessagePersistsConfidenceThresholdReason(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	dlqProducer, dlqTopic, auditTopic := testProducer(t)
	batchID, recordID := seedRecord(ctx, t, pool)

	// A live-model-shaped response: recommends RETRY (not ESCALATE), but at
	// a confidence the configured threshold does not trust.
	classifier := &fakeClassifier{resp: &classifierv1.ClassifyResponse{
		Bucket:            commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK,
		RecommendedAction: commonv1.ActionType_ACTION_TYPE_RETRY,
		Rationale:         "plausible but unsure",
		Confidence:        0.3,
		Source:            commonv1.Source_SOURCE_LLM,
	}}
	cfg := testConfig(dlqTopic, auditTopic)
	cfg.ClassifyConfidenceThreshold = 0.5
	e := New(pool, classifier, &fakeExecutor{}, dlqProducer, clock.New(), testEconomics(t), cfg)

	msg := rawEventMessage(t, RawEvent{RecordID: recordID, BatchID: batchID, Type: "RECORD_TYPE_PAYMENT", AmountPaise: 10000, FailureCode: "BANK_TIMEOUT"})
	if err := e.HandleMessage(ctx, msg); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}

	var state, reason string
	if err := pool.QueryRow(ctx, `SELECT current_state FROM record_state WHERE record_id = $1`, recordID).Scan(&state); err != nil {
		t.Fatalf("query record_state: %v", err)
	}
	if state != commonv1.RecordState_RECORD_STATE_ESCALATED.String() {
		t.Fatalf("current_state = %q, want ESCALATED", state)
	}
	if err := pool.QueryRow(ctx, `SELECT reason FROM audit_entry WHERE record_id = $1 AND to_state = $2`,
		recordID, commonv1.RecordState_RECORD_STATE_ESCALATED.String()).Scan(&reason); err != nil {
		t.Fatalf("query audit_entry: %v", err)
	}
	if reason != "classification confidence below threshold" {
		t.Errorf("audit_entry.reason = %q, want %q", reason, "classification confidence below threshold")
	}
}

// The unknown-code path (rules/actions.go's ROOT_CAUSE_BUCKET_UNSPECIFIED
// row) always returns confidence 0.0 and recommends ESCALATE, so it
// satisfies the new confidence check too once a threshold is configured.
// The audit trail must still say the escalation was the classifier's
// recommendation, not blame a confidence threshold that happens to also be
// true -- otherwise "we do not recognise this failure code" and "the model
// was unsure" become indistinguishable in the one place an operator would
// look.
func TestHandleMessagePersistsUnknownCodeReasonNotConfidenceReason(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	dlqProducer, dlqTopic, auditTopic := testProducer(t)
	batchID, recordID := seedRecord(ctx, t, pool)

	classifier := &fakeClassifier{resp: &classifierv1.ClassifyResponse{
		Bucket:            commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_UNSPECIFIED,
		RecommendedAction: commonv1.ActionType_ACTION_TYPE_ESCALATE,
		Rationale:         "unrecognised failure code XYZ",
		Confidence:        0.0,
		Source:            commonv1.Source_SOURCE_RULES_FALLBACK,
	}}
	cfg := testConfig(dlqTopic, auditTopic)
	cfg.ClassifyConfidenceThreshold = 0.5
	e := New(pool, classifier, &fakeExecutor{}, dlqProducer, clock.New(), testEconomics(t), cfg)

	msg := rawEventMessage(t, RawEvent{RecordID: recordID, BatchID: batchID, Type: "RECORD_TYPE_PAYMENT", AmountPaise: 10000, FailureCode: "XYZ"})
	if err := e.HandleMessage(ctx, msg); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}

	var reason string
	if err := pool.QueryRow(ctx, `SELECT reason FROM audit_entry WHERE record_id = $1 AND to_state = $2`,
		recordID, commonv1.RecordState_RECORD_STATE_ESCALATED.String()).Scan(&reason); err != nil {
		t.Fatalf("query audit_entry: %v", err)
	}
	if reason != "classifier recommended escalation" {
		t.Errorf("audit_entry.reason = %q, want %q (this is the unknown-code path, not a genuinely low-confidence model answer)", reason, "classifier recommended escalation")
	}
}
