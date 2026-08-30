package server

import (
	"context"
	"errors"
	"strings"
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

// newTestNudgeChain wires the real static template behind a template-only
// nudge chain, the ComposeNudge equivalent of newTestServer's rationale:
// TemplateNudgeProvider is pure and cannot fail.
func newTestNudgeChain(t *testing.T) *provider.NudgeChain {
	t.Helper()
	log := logger.Discard()
	c, err := provider.NewNudgeChain(
		[]string{provider.RulesName},
		map[string]provider.NudgeProvider{provider.RulesName: rules.NewTemplateNudgeProvider()},
		provider.Config{RungTimeout: 2 * time.Second, Reserve: 150 * time.Millisecond},
		log,
	)
	if err != nil {
		t.Fatalf("build nudge chain: %v", err)
	}
	return c
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
	nudgeFallback := prometheus.NewCounter(prometheus.CounterOpts{Name: "test_nudge_fallback_total"})
	return New(c, newTestNudgeChain(t), log, fallback, nudgeFallback), fallback
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
	nudgeFallback := prometheus.NewCounter(prometheus.CounterOpts{Name: "test_nudge_fallback_total"})
	return New(c, newTestNudgeChain(t), log, fallback, nudgeFallback), fallback
}

// failingNudgeProvider always errors, the ComposeNudge equivalent of
// failingProvider, so a test can force a nudge chain to attempt an LLM rung
// and fall through to the template.
type failingNudgeProvider struct{}

func (failingNudgeProvider) Name() string { return "groq" }
func (failingNudgeProvider) ComposeNudge(ctx context.Context, req *classifierv1.ComposeNudgeRequest) (*classifierv1.ComposeNudgeResponse, error) {
	return nil, errors.New("failingNudgeProvider always fails")
}

// newTestServerWithNudgeFallbackCounter mirrors newTestServerWithCounter,
// but for nudge_fallback_total: the nudge chain has two rungs, groq (always
// fails) then the template, so a call proves the counter's actual condition.
func newTestServerWithNudgeFallbackCounter(t *testing.T) (*Server, prometheus.Counter) {
	t.Helper()
	log := logger.Discard()
	classifyChain, err := provider.NewChain(
		[]string{provider.RulesName},
		map[string]provider.Provider{provider.RulesName: rules.New(log)},
		provider.Config{RungTimeout: 2 * time.Second, Reserve: 150 * time.Millisecond},
		log,
	)
	if err != nil {
		t.Fatalf("build chain: %v", err)
	}
	nudgeChain, err := provider.NewNudgeChain(
		[]string{"groq", provider.RulesName},
		map[string]provider.NudgeProvider{"groq": failingNudgeProvider{}, provider.RulesName: rules.NewTemplateNudgeProvider()},
		provider.Config{RungTimeout: 2 * time.Second, Reserve: 150 * time.Millisecond},
		log,
	)
	if err != nil {
		t.Fatalf("build nudge chain: %v", err)
	}
	fallback := prometheus.NewCounter(prometheus.CounterOpts{Name: "test_llm_fallback_total"})
	nudgeFallback := prometheus.NewCounter(prometheus.CounterOpts{Name: "test_nudge_fallback_total"})
	return New(classifyChain, nudgeChain, log, fallback, nudgeFallback), nudgeFallback
}

// newTestServerWithTemplateOnlyNudgeFallbackCounter mirrors
// newRulesOnlyTestServer, but exposes the nudge fallback counter: the nudge
// chain never contains anything but the template, so no LLM rung was ever
// offered the record.
func newTestServerWithTemplateOnlyNudgeFallbackCounter(t *testing.T) (*Server, prometheus.Counter) {
	t.Helper()
	log := logger.Discard()
	classifyChain, err := provider.NewChain(
		[]string{provider.RulesName},
		map[string]provider.Provider{provider.RulesName: rules.New(log)},
		provider.Config{RungTimeout: 2 * time.Second, Reserve: 150 * time.Millisecond},
		log,
	)
	if err != nil {
		t.Fatalf("build chain: %v", err)
	}
	fallback := prometheus.NewCounter(prometheus.CounterOpts{Name: "test_llm_fallback_total"})
	nudgeFallback := prometheus.NewCounter(prometheus.CounterOpts{Name: "test_nudge_fallback_total"})
	return New(classifyChain, newTestNudgeChain(t), log, fallback, nudgeFallback), nudgeFallback
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

func TestComposeNudgeRejectsNilRecord(t *testing.T) {
	s := newTestServer(t)

	_, err := s.ComposeNudge(context.Background(), &classifierv1.ComposeNudgeRequest{MaxChars: 160})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("ComposeNudge with nil record: err = %v, want InvalidArgument", err)
	}
}

func TestComposeNudgeRejectsEmptyRecordID(t *testing.T) {
	s := newTestServer(t)

	_, err := s.ComposeNudge(context.Background(), &classifierv1.ComposeNudgeRequest{
		Record:   &commonv1.Record{AmountPaise: 100000},
		MaxChars: 160,
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("ComposeNudge with empty record.id: err = %v, want InvalidArgument", err)
	}
}

// TestComposeNudgeReturnsTheTemplateWhenTheChainIsTemplateOnly is the
// happy-path equivalent of TestClassifyReturnsFullyPopulatedResponse: the
// chain answers, and Source/Hops are populated by the chain layer, not left
// zero.
func TestComposeNudgeReturnsTheTemplateWhenTheChainIsTemplateOnly(t *testing.T) {
	s := newTestServer(t)

	resp, err := s.ComposeNudge(context.Background(), &classifierv1.ComposeNudgeRequest{
		Record:   &commonv1.Record{Id: "rec-1", AmountPaise: 499900},
		Bucket:   commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK,
		MaxChars: 160,
	})
	if err != nil {
		t.Fatalf("ComposeNudge: %v", err)
	}
	if resp.GetMessage() == "" {
		t.Error("Message is empty")
	}
	if resp.GetSource() != commonv1.Source_SOURCE_RULES_FALLBACK {
		t.Errorf("Source = %v, want SOURCE_RULES_FALLBACK", resp.GetSource())
	}
	if len(resp.GetHops()) != 1 {
		t.Errorf("hops = %+v, want exactly one", resp.GetHops())
	}
}

// TestComposeNudgeSubstitutesTheRealAmount is the property ARCHITECTURE.md
// section 5b exists for: "the record's real amount ... interpolated by us,
// not written by the model [or the template]". The literal placeholder
// token must never reach the caller.
func TestComposeNudgeSubstitutesTheRealAmount(t *testing.T) {
	s := newTestServer(t)

	resp, err := s.ComposeNudge(context.Background(), &classifierv1.ComposeNudgeRequest{
		Record:   &commonv1.Record{Id: "rec-1", AmountPaise: 499900},
		Bucket:   commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK,
		MaxChars: 160,
	})
	if err != nil {
		t.Fatalf("ComposeNudge: %v", err)
	}
	if strings.Contains(resp.GetMessage(), "{{AMOUNT}}") {
		t.Errorf("Message = %q, still contains the literal placeholder", resp.GetMessage())
	}
	if !strings.Contains(resp.GetMessage(), "4999") {
		t.Errorf("Message = %q, want the real amount (499900 paise = Rs 4999) substituted in", resp.GetMessage())
	}
}

// TestComposeNudgeIncrementsNudgeFallbackCounterWhenLLMRungFailed and
// TestComposeNudgeDoesNotIncrementNudgeFallbackCounterForTemplateOnlyChain
// mirror the Classify pair exactly (docs/ARCHITECTURE.md section 13's
// llm_fallback_total, extended to nudge composition as nudge_fallback_total).
func TestComposeNudgeIncrementsNudgeFallbackCounterWhenLLMRungFailed(t *testing.T) {
	s, nudgeFallback := newTestServerWithNudgeFallbackCounter(t)

	req := &classifierv1.ComposeNudgeRequest{
		Record:   &commonv1.Record{Id: "rec-1", AmountPaise: 100000},
		Bucket:   commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK,
		MaxChars: 160,
	}
	if _, err := s.ComposeNudge(context.Background(), req); err != nil {
		t.Fatalf("ComposeNudge: %v", err)
	}
	if got := testutil.ToFloat64(nudgeFallback); got != 1 {
		t.Fatalf("nudge_fallback_total after 1 call with a failed LLM rung = %v, want 1", got)
	}
}

func TestComposeNudgeDoesNotIncrementNudgeFallbackCounterForTemplateOnlyChain(t *testing.T) {
	s, nudgeFallback := newTestServerWithTemplateOnlyNudgeFallbackCounter(t)

	req := &classifierv1.ComposeNudgeRequest{
		Record:   &commonv1.Record{Id: "rec-1", AmountPaise: 100000},
		Bucket:   commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK,
		MaxChars: 160,
	}
	if _, err := s.ComposeNudge(context.Background(), req); err != nil {
		t.Fatalf("ComposeNudge: %v", err)
	}
	if got := testutil.ToFloat64(nudgeFallback); got != 0 {
		t.Errorf("nudge_fallback_total with no LLM rung in the nudge chain = %v, want 0", got)
	}
}
