package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/thisizaro/Momotaro/internal/platform/logger"
	classifierv1 "github.com/thisizaro/Momotaro/proto/gen/classifier/v1"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	"github.com/thisizaro/Momotaro/services/classifier/internal/provider"
	"github.com/thisizaro/Momotaro/services/classifier/internal/rules"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// failingProvider is a Provider that always errors, standing in for a real
// LLM rung so tests can force a chain to actually attempt one and fall
// through to rules, rather than the rules engine being the only rung in
// play (SPEC.md section 4.7: the chain tries the next rung only if the
// previous one failed).
type failingProvider struct{}

func (failingProvider) Name() string { return "groq" }
func (failingProvider) Classify(ctx context.Context, req *classifierv1.ClassifyRequest) (*classifierv1.ClassifyResponse, error) {
	return nil, errors.New("failingProvider always fails")
}

// newTestServer wires the real rules engine behind a rules-only chain: the
// rules engine is pure and cannot fail, so there is no need for a fake here
// except at the chain interface boundary, which exists for that purpose.
func newTestServer(t *testing.T) *Server {
	t.Helper()
	s, _ := newTestServerWithCounter(t)
	return s
}

// newTestServerWithCounter is newTestServer plus the fallback counter, for
// the tests that need to read it. The chain has two rungs, groq (always
// fails) then rules, not rules-only: every call here answers via
// SOURCE_RULES_FALLBACK either way, but only a two-rung chain can prove the
// counter's actual condition (an LLM rung was attempted) rather than
// happening to pass in a case where it is vacuously true. A fresh,
// unregistered counter each call: nothing here needs a real registry.
func newTestServerWithCounter(t *testing.T) (*Server, prometheus.Counter) {
	t.Helper()
	log := logger.Discard()
	c, err := provider.NewChain(
		[]string{"groq", provider.RulesName},
		map[string]provider.Provider{"groq": failingProvider{}, provider.RulesName: rules.New(log)},
		provider.Config{RungTimeout: 2 * time.Second, Reserve: 150 * time.Millisecond},
		log,
	)
	if err != nil {
		t.Fatalf("build chain: %v", err)
	}
	fallback := prometheus.NewCounter(prometheus.CounterOpts{Name: "test_llm_fallback_total"})
	return New(c, log, fallback), fallback
}

// newRulesOnlyTestServer mirrors force_rules_only's actual runtime shape
// (SPEC.md section 4.8): the chain never contains anything but rules, so no
// LLM rung was ever offered the record. Distinct from newTestServer's
// two-rung chain on purpose: the fallback counter must NOT increment here,
// only when an LLM rung was attempted and failed.
func newRulesOnlyTestServer(t *testing.T) (*Server, prometheus.Counter) {
	t.Helper()
	log := logger.Discard()
	c, err := provider.NewChain(
		[]string{provider.RulesName},
		map[string]provider.Provider{provider.RulesName: rules.New(log)},
		provider.Config{RungTimeout: 2 * time.Second, Reserve: 150 * time.Millisecond},
		log,
	)
	if err != nil {
		t.Fatalf("build chain: %v", err)
	}
	fallback := prometheus.NewCounter(prometheus.CounterOpts{Name: "test_llm_fallback_total"})
	return New(c, log, fallback), fallback
}

func TestClassifyRejectsNilRecord(t *testing.T) {
	s := newTestServer(t)

	_, err := s.Classify(context.Background(), &classifierv1.ClassifyRequest{})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("Classify with nil record: err = %v, want InvalidArgument", err)
	}
}

func TestClassifyRejectsEmptyRecordID(t *testing.T) {
	s := newTestServer(t)

	_, err := s.Classify(context.Background(), &classifierv1.ClassifyRequest{
		Record: &commonv1.Record{FailureCode: "BANK_TIMEOUT"},
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("Classify with empty record.id: err = %v, want InvalidArgument", err)
	}
}

func TestClassifyReturnsFullyPopulatedResponse(t *testing.T) {
	s := newTestServer(t)

	resp, err := s.Classify(context.Background(), &classifierv1.ClassifyRequest{
		Record: &commonv1.Record{Id: "rec-1", FailureCode: "BANK_TIMEOUT"},
	})
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if resp.GetBucket() == commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_UNSPECIFIED {
		t.Error("Bucket left UNSPECIFIED for a recognised code")
	}
	if resp.GetRecommendedAction() == commonv1.ActionType_ACTION_TYPE_UNSPECIFIED {
		t.Error("RecommendedAction left UNSPECIFIED")
	}
	if resp.GetRationale() == "" {
		t.Error("Rationale is empty")
	}
	if resp.GetSource() == commonv1.Source_SOURCE_UNSPECIFIED {
		t.Error("Source left UNSPECIFIED")
	}
	if len(resp.GetHops()) == 0 {
		t.Error("no provider hops recorded")
	}
}

// The chain here has a real (failing) LLM rung ahead of rules, so this is
// a genuine "the LLM was attempted and failed" fallback: the counter must
// increment once per call.
func TestClassifyIncrementsFallbackCounterWhenLLMRungFailed(t *testing.T) {
	s, fallback := newTestServerWithCounter(t)
	req := &classifierv1.ClassifyRequest{Record: &commonv1.Record{Id: "rec-1", FailureCode: "BANK_TIMEOUT"}}

	if _, err := s.Classify(context.Background(), req); err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if got := testutil.ToFloat64(fallback); got != 1 {
		t.Fatalf("llm_fallback_total after 1 call with a failed LLM rung = %v, want 1", got)
	}

	if _, err := s.Classify(context.Background(), req); err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if got := testutil.ToFloat64(fallback); got != 2 {
		t.Fatalf("llm_fallback_total after 2 calls with a failed LLM rung = %v, want 2", got)
	}
}

// The case the counter exists to get right: force_rules_only (or a chain
// simply configured as rules-only) never offers the record to an LLM rung
// at all, so this must NOT count as a fallback. Conflating the two would
// make docs/ARCHITECTURE.md section 13's llm_fallback_total alert fire
// constantly on any config sampling less than 100% of records for a live
// model call (docs/PHASE3_IMPLEMENTATION.md Unit H), which is normal
// operation, not degradation.
func TestClassifyDoesNotIncrementFallbackCounterWhenNoLLMRungExists(t *testing.T) {
	s, fallback := newRulesOnlyTestServer(t)
	req := &classifierv1.ClassifyRequest{Record: &commonv1.Record{Id: "rec-1", FailureCode: "BANK_TIMEOUT"}}

	if _, err := s.Classify(context.Background(), req); err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if got := testutil.ToFloat64(fallback); got != 0 {
		t.Errorf("llm_fallback_total with no LLM rung in the chain = %v, want 0", got)
	}
}

// Empty history/instrument_history is the production path today (SPEC.md
// section 3: the Decision Engine never populates either field), so it is
// tested explicitly rather than assumed to work.
func TestClassifyHandlesEmptyHistoryWithoutError(t *testing.T) {
	s := newTestServer(t)

	_, err := s.Classify(context.Background(), &classifierv1.ClassifyRequest{
		Record:            &commonv1.Record{Id: "rec-1", FailureCode: "BANK_TIMEOUT"},
		History:           nil,
		InstrumentHistory: nil,
	})
	if err != nil {
		t.Fatalf("Classify with empty history: %v", err)
	}
}

func TestComposeNudgeIsUnimplemented(t *testing.T) {
	s := newTestServer(t)

	_, err := s.ComposeNudge(context.Background(), &classifierv1.ComposeNudgeRequest{})
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("ComposeNudge: err = %v, want Unimplemented", err)
	}
}
