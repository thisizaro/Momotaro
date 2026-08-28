//go:build integration

// Phase 3 Unit H: LLM_SAMPLE_RATE decides, per record, whether
// ClassifyRequest.force_rules_only is set. sampling_test.go proves the
// hash-based decision itself; this proves it actually reaches the wire
// through HandleMessage, not just that the helper function returns the
// right bool in isolation.
package engine

import (
	"context"
	"testing"

	"github.com/thisizaro/Momotaro/internal/platform/clock"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

func TestHandleMessageForcesRulesOnlyAtDefaultSampleRate(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	dlqProducer, dlqTopic := testDLQ(t)
	batchID, recordID := seedRecord(ctx, t, pool)

	classifier := &fakeClassifier{resp: classifyResponseWithAction(commonv1.ActionType_ACTION_TYPE_ESCALATE)}
	cfg := testConfig(dlqTopic) // LLMSampleRate defaults to the zero value, 0.0
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
		t.Error("ForceRulesOnly = false at LLM_SAMPLE_RATE=0.0, want true: no record should ever be sampled at the default rate")
	}
}

func TestHandleMessageAllowsLiveCallAtFullSampleRate(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	dlqProducer, dlqTopic := testDLQ(t)
	batchID, recordID := seedRecord(ctx, t, pool)

	classifier := &fakeClassifier{resp: classifyResponseWithAction(commonv1.ActionType_ACTION_TYPE_ESCALATE)}
	cfg := testConfig(dlqTopic)
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
		t.Error("ForceRulesOnly = true at LLM_SAMPLE_RATE=1.0, want false: every record should be sampled at rate 1.0")
	}
}
