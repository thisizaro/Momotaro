package engine

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	classifierv1 "github.com/thisizaro/Momotaro/proto/gen/classifier/v1"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	"google.golang.org/grpc"
)

// stubClassifier is a plain-unit fake of classifierv1.ClassifierServiceClient
// (no Postgres, no Kafka), so clients.classify's routing decision can be
// tested without the `integration` build tag. It answers differently
// depending on ForceRulesOnly, the way the real provider chain does: the
// rules-only "peek" call gets rulesResp, a full-chain call gets liveResp,
// so a test can tell which path clients.classify actually took.
type stubClassifier struct {
	mu    sync.Mutex
	calls []*classifierv1.ClassifyRequest

	rulesResp *classifierv1.ClassifyResponse
	liveResp  *classifierv1.ClassifyResponse
	err       error
}

func (s *stubClassifier) Classify(ctx context.Context, in *classifierv1.ClassifyRequest, opts ...grpc.CallOption) (*classifierv1.ClassifyResponse, error) {
	s.mu.Lock()
	s.calls = append(s.calls, in)
	s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	if in.GetForceRulesOnly() {
		return s.rulesResp, nil
	}
	return s.liveResp, nil
}

func (s *stubClassifier) ComposeNudge(ctx context.Context, in *classifierv1.ComposeNudgeRequest, opts ...grpc.CallOption) (*classifierv1.ComposeNudgeResponse, error) {
	return &classifierv1.ComposeNudgeResponse{Message: "unused"}, nil
}

func (s *stubClassifier) requestCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

func (s *stubClassifier) requestAt(i int) *classifierv1.ClassifyRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[i]
}

func confidentResp(confidence float64) *classifierv1.ClassifyResponse {
	return &classifierv1.ClassifyResponse{
		Bucket:            commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK,
		RecommendedAction: commonv1.ActionType_ACTION_TYPE_RETRY,
		Rationale:         "test rationale",
		Confidence:        confidence,
		Source:            commonv1.Source_SOURCE_RULES_FALLBACK,
		Hops:              []*commonv1.ProviderHop{{Provider: "rules", Result: "ok"}},
	}
}

func testClients(classifier *stubClassifier, threshold float64, rate float64) *clients {
	return &clients{
		classifier:               classifier,
		callTimeout:              2 * time.Second,
		routeConfidenceThreshold: threshold,
		llmBudget:                newLLMBudget(rate),
	}
}

func testRecord() *commonv1.Record {
	return &commonv1.Record{Id: "rec-1", Type: commonv1.RecordType_RECORD_TYPE_PAYMENT, AmountPaise: 10000, FailureCode: "BANK_TIMEOUT"}
}

// A record the rules engine is confident about (confidence >= threshold)
// must never reach a live model, whatever the budget: routing is decided
// by confidence first, the budget only gates the ambiguous ones.
func TestClassifyConfidentRecordSkipsModel(t *testing.T) {
	classifier := &stubClassifier{rulesResp: confidentResp(0.95), liveResp: confidentResp(0.5)}
	c := testClients(classifier, 0.80, 1.0)

	resp, err := c.classify(context.Background(), testRecord(), nil, nil)
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if got := classifier.requestCount(); got != 1 {
		t.Fatalf("classifier received %d requests, want 1: a confident record must never place a second, live-model call", got)
	}
	if !classifier.requestAt(0).GetForceRulesOnly() {
		t.Error("the one request made had ForceRulesOnly = false, want true (the rules-only peek)")
	}
	if resp.GetConfidence() != 0.95 {
		t.Errorf("returned response confidence = %v, want the rules answer's 0.95", resp.GetConfidence())
	}
}

// A record the rules engine is NOT confident about, with budget available,
// must place a second, live-model call and use its answer.
func TestClassifyAmbiguousRecordWithBudgetCallsModel(t *testing.T) {
	classifier := &stubClassifier{rulesResp: confidentResp(0.5), liveResp: confidentResp(0.9)}
	c := testClients(classifier, 0.80, 1.0)

	resp, err := c.classify(context.Background(), testRecord(), nil, nil)
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if got := classifier.requestCount(); got != 2 {
		t.Fatalf("classifier received %d requests, want 2: an ambiguous record within budget must place a live call after the peek", got)
	}
	if !classifier.requestAt(0).GetForceRulesOnly() {
		t.Error("first request had ForceRulesOnly = false, want true (the rules-only peek)")
	}
	if classifier.requestAt(1).GetForceRulesOnly() {
		t.Error("second request had ForceRulesOnly = true, want false (the live model call)")
	}
	if resp.GetConfidence() != 0.9 {
		t.Errorf("returned response confidence = %v, want the live answer's 0.9", resp.GetConfidence())
	}
}

// A record the rules engine is NOT confident about, with the budget
// already exhausted (rate 0.0 here), must fall back to the rules answer
// rather than place a live call, and the fallback must be marked so
// Reporting can count it as quota exhaustion (docs/DEMO_READINESS.md Unit
// AI, services/reporting/internal/server/exhaustion.go).
func TestClassifyAmbiguousRecordWithoutBudgetFallsBackAndMarksExhaustion(t *testing.T) {
	classifier := &stubClassifier{rulesResp: confidentResp(0.5), liveResp: confidentResp(0.9)}
	c := testClients(classifier, 0.80, 0.0)

	resp, err := c.classify(context.Background(), testRecord(), nil, nil)
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if got := classifier.requestCount(); got != 1 {
		t.Fatalf("classifier received %d requests, want 1: an ambiguous record outside budget must not place a live call", got)
	}
	if resp.GetConfidence() != 0.5 {
		t.Errorf("returned response confidence = %v, want the rules answer's 0.5 (the fallback)", resp.GetConfidence())
	}
	found := false
	for _, h := range resp.GetHops() {
		if h.GetResult() == "exhausted" {
			found = true
		}
	}
	if !found {
		t.Errorf("hops = %v, want an \"exhausted\" hop marking the budget-denied fallback", resp.GetHops())
	}
}

// A confident record must never carry an "exhausted" hop: it was never
// eligible for a live call, so there is nothing it was denied.
func TestClassifyConfidentRecordNeverMarkedExhausted(t *testing.T) {
	classifier := &stubClassifier{rulesResp: confidentResp(0.95), liveResp: confidentResp(0.5)}
	c := testClients(classifier, 0.80, 0.0)

	resp, err := c.classify(context.Background(), testRecord(), nil, nil)
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	for _, h := range resp.GetHops() {
		if h.GetResult() == "exhausted" {
			t.Errorf("hops = %v, a confident record must never be marked exhausted", resp.GetHops())
		}
	}
}

// A failing peek call must surface its error without attempting a second,
// live-model call.
func TestClassifyPeekErrorReturnsWithoutSecondCall(t *testing.T) {
	wantErr := errors.New("boom")
	classifier := &stubClassifier{err: wantErr}
	c := testClients(classifier, 0.80, 1.0)

	_, err := c.classify(context.Background(), testRecord(), nil, nil)
	if !errors.Is(err, wantErr) {
		t.Fatalf("classify err = %v, want %v", err, wantErr)
	}
	if got := classifier.requestCount(); got != 1 {
		t.Errorf("classifier received %d requests, want 1: a failing peek must not be followed by a second call", got)
	}
}

// The budget ceiling is respected across a sequence of records, not just
// per-call: at rate 0.5, with every record ambiguous, only half should
// ever reach a live call.
func TestClassifyBudgetCeilingRespectedAcrossRecords(t *testing.T) {
	classifier := &stubClassifier{rulesResp: confidentResp(0.5), liveResp: confidentResp(0.9)}
	c := testClients(classifier, 0.80, 0.5)

	liveCalls := 0
	const n = 20
	for i := 0; i < n; i++ {
		classifier.mu.Lock()
		classifier.calls = nil
		classifier.mu.Unlock()
		if _, err := c.classify(context.Background(), testRecord(), nil, nil); err != nil {
			t.Fatalf("classify record %d: %v", i, err)
		}
		if classifier.requestCount() == 2 {
			liveCalls++
		}
	}
	if liveCalls > n/2 {
		t.Errorf("live calls = %d out of %d records at rate 0.5, want at most %d", liveCalls, n, n/2)
	}
}
