//go:build integration

// docs/DEMO_READINESS.md Unit AI replaced Phase 3 Unit H's
// hash-of-record-id sampledForLLM (which this file used to test directly)
// with two separate knobs: RouteConfidenceThreshold decides WHICH records
// are ambiguous enough to want a live model call, and LLMSampleRate is now
// a ceiling on HOW MANY of those actually get one, never a selector on its
// own. clients_test.go and llm_budget_test.go prove the two mechanisms in
// isolation without a database; this file proves the ceiling actually
// reaches the wire through HandleMessage, the same way this file always
// has for LLM_SAMPLE_RATE.
package engine

import (
	"context"
	"testing"

	"github.com/thisizaro/Momotaro/internal/platform/clock"
	classifierv1 "github.com/thisizaro/Momotaro/proto/gen/classifier/v1"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

// ambiguousClassifyResponse is confidently NOT confident: RouteConfidenceThreshold
// set above 0.4 makes every record classified this way eligible for a live
// call, so these tests isolate the budget ceiling's own behaviour rather
// than routing's.
func ambiguousClassifyResponse(action commonv1.ActionType) *classifierv1.ClassifyResponse {
	resp := classifyResponseWithAction(action)
	resp.Confidence = 0.4
	return resp
}

func TestHandleMessageForcesRulesOnlyWhenSampleBudgetIsZero(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	dlqProducer, dlqTopic, auditTopic := testProducer(t)
	batchID, recordID := seedRecord(ctx, t, pool)

	classifier := &fakeClassifier{resp: ambiguousClassifyResponse(commonv1.ActionType_ACTION_TYPE_ESCALATE)}
	cfg := testConfig(dlqTopic, auditTopic) // LLMSampleRate defaults to the zero value, 0.0
	cfg.RouteConfidenceThreshold = 0.8      // the response's 0.4 confidence is ambiguous at this threshold
	e := New(pool, classifier, &fakeExecutor{}, dlqProducer, clock.New(), testEconomics(t), cfg)

	msg := rawEventMessage(t, RawEvent{RecordID: recordID, BatchID: batchID, Type: "RECORD_TYPE_PAYMENT", AmountPaise: 10000, FailureCode: "RISK_HOLD"})
	if err := e.HandleMessage(ctx, msg); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}

	req := classifier.lastRequest()
	if req == nil {
		t.Fatal("classifier never received a ClassifyRequest")
	}
	if !req.GetForceRulesOnly() {
		t.Error("ForceRulesOnly = false at LLM_SAMPLE_RATE=0.0, want true: an ambiguous record must still fall back to rules once the budget is spent (here, never available at all)")
	}
}

func TestHandleMessageAllowsLiveCallAtFullSampleBudget(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	dlqProducer, dlqTopic, auditTopic := testProducer(t)
	batchID, recordID := seedRecord(ctx, t, pool)

	classifier := &fakeClassifier{resp: ambiguousClassifyResponse(commonv1.ActionType_ACTION_TYPE_ESCALATE)}
	cfg := testConfig(dlqTopic, auditTopic)
	cfg.RouteConfidenceThreshold = 0.8 // ambiguous at 0.4 confidence
	cfg.LLMSampleRate = 1.0
	e := New(pool, classifier, &fakeExecutor{}, dlqProducer, clock.New(), testEconomics(t), cfg)

	msg := rawEventMessage(t, RawEvent{RecordID: recordID, BatchID: batchID, Type: "RECORD_TYPE_PAYMENT", AmountPaise: 10000, FailureCode: "RISK_HOLD"})
	if err := e.HandleMessage(ctx, msg); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}

	req := classifier.lastRequest()
	if req == nil {
		t.Fatal("classifier never received a ClassifyRequest")
	}
	if req.GetForceRulesOnly() {
		t.Error("ForceRulesOnly = true at LLM_SAMPLE_RATE=1.0, want false: an ambiguous record with full budget must place a live call")
	}
	if got := classifier.calls; got != 2 {
		t.Errorf("classifier.calls = %d, want 2: the confidence peek, then the live call the budget allowed", got)
	}
}

// A confident record must stay rules-only even at LLM_SAMPLE_RATE=1.0:
// the budget only gates ambiguous records, it never overrides routing.
func TestHandleMessageConfidentRecordStaysRulesOnlyDespiteFullSampleBudget(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	dlqProducer, dlqTopic, auditTopic := testProducer(t)
	batchID, recordID := seedRecord(ctx, t, pool)

	classifier := &fakeClassifier{resp: classifyResponseWithAction(commonv1.ActionType_ACTION_TYPE_ESCALATE)} // Confidence: 1
	cfg := testConfig(dlqTopic, auditTopic)
	cfg.RouteConfidenceThreshold = 0.8
	cfg.LLMSampleRate = 1.0
	e := New(pool, classifier, &fakeExecutor{}, dlqProducer, clock.New(), testEconomics(t), cfg)

	msg := rawEventMessage(t, RawEvent{RecordID: recordID, BatchID: batchID, Type: "RECORD_TYPE_PAYMENT", AmountPaise: 10000, FailureCode: "RISK_HOLD"})
	if err := e.HandleMessage(ctx, msg); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}

	if got := classifier.calls; got != 1 {
		t.Errorf("classifier.calls = %d, want 1: a confident record (confidence 1.0 >= threshold 0.8) must never place a live call, whatever the budget", got)
	}
	req := classifier.lastRequest()
	if req == nil {
		t.Fatal("classifier never received a ClassifyRequest")
	}
	if !req.GetForceRulesOnly() {
		t.Error("ForceRulesOnly = false for a confident record, want true")
	}
}
