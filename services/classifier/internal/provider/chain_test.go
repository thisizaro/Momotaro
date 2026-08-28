package provider

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/thisizaro/Momotaro/internal/platform/logger"
	classifierv1 "github.com/thisizaro/Momotaro/proto/gen/classifier/v1"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

// fakeRung is a test-only Provider. Real rungs never appear in this
// package's tests: SPEC.md section 7 requires the chain be tested with
// fakes, never a real call.
type fakeRung struct {
	name  string
	calls int
	resp  *classifierv1.ClassifyResponse
	err   error
}

func (f *fakeRung) Name() string { return f.name }

func (f *fakeRung) Classify(ctx context.Context, req *classifierv1.ClassifyRequest) (*classifierv1.ClassifyResponse, error) {
	f.calls++
	return f.resp, f.err
}

// testConfig is a generous budget: these tests are about chain behaviour, not
// about timing. The tests that care about the budget build their own Config.
func testConfig() Config {
	return Config{RungTimeout: 2 * time.Second, Reserve: 150 * time.Millisecond}
}

func validResponse() *classifierv1.ClassifyResponse {
	return &classifierv1.ClassifyResponse{
		Bucket:            commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK,
		RecommendedAction: commonv1.ActionType_ACTION_TYPE_RETRY,
		Rationale:         "test rationale",
		Confidence:        0.9,
	}
}

func invalidResponses() map[string]*classifierv1.ClassifyResponse {
	return map[string]*classifierv1.ClassifyResponse{
		"bucket outside enum": {
			Bucket: commonv1.RootCauseBucket(999), RecommendedAction: commonv1.ActionType_ACTION_TYPE_RETRY,
			Rationale: "x", Confidence: 0.5,
		},
		"action outside menu": {
			Bucket: commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK, RecommendedAction: commonv1.ActionType(999),
			Rationale: "x", Confidence: 0.5,
		},
		"confidence above range": {
			Bucket: commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK, RecommendedAction: commonv1.ActionType_ACTION_TYPE_RETRY,
			Rationale: "x", Confidence: 1.5,
		},
		"confidence below range": {
			Bucket: commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK, RecommendedAction: commonv1.ActionType_ACTION_TYPE_RETRY,
			Rationale: "x", Confidence: -0.1,
		},
	}
}

func TestValidate(t *testing.T) {
	if err := validate(validResponse()); err != nil {
		t.Errorf("validate(valid response): %v", err)
	}
	for name, bad := range invalidResponses() {
		t.Run(name, func(t *testing.T) {
			if err := validate(bad); err == nil {
				t.Errorf("validate(%s): want error, got nil", name)
			}
		})
	}
}

func TestChainSingleRulesRung(t *testing.T) {
	rules := &fakeRung{name: RulesName, resp: validResponse()}
	c, err := NewChain([]string{RulesName}, map[string]Provider{RulesName: rules}, testConfig(), logger.Discard())
	if err != nil {
		t.Fatalf("NewChain: %v", err)
	}

	resp, err := c.Classify(context.Background(), &classifierv1.ClassifyRequest{})
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if resp.GetSource() != commonv1.Source_SOURCE_RULES_FALLBACK {
		t.Errorf("Source = %v, want SOURCE_RULES_FALLBACK", resp.GetSource())
	}
	hops := resp.GetHops()
	if len(hops) != 1 || hops[0].GetProvider() != RulesName || hops[0].GetResult() != "ok" {
		t.Errorf("hops = %+v, want exactly [{rules ok}]", hops)
	}
}

func TestChainRungErrorFallsThroughToNextRung(t *testing.T) {
	failing := &fakeRung{name: "llm", err: errors.New("boom")}
	rules := &fakeRung{name: RulesName, resp: validResponse()}
	c, err := NewChain([]string{"llm", RulesName}, map[string]Provider{"llm": failing, RulesName: rules}, testConfig(), logger.Discard())
	if err != nil {
		t.Fatalf("NewChain: %v", err)
	}

	resp, err := c.Classify(context.Background(), &classifierv1.ClassifyRequest{})
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if failing.calls != 1 {
		t.Errorf("failing rung calls = %d, want 1", failing.calls)
	}
	if rules.calls != 1 {
		t.Errorf("next rung calls = %d, want 1", rules.calls)
	}
	hops := resp.GetHops()
	if len(hops) != 2 || hops[0].GetProvider() != "llm" || hops[0].GetResult() != "error" ||
		hops[1].GetProvider() != RulesName || hops[1].GetResult() != "ok" {
		t.Errorf("hops = %+v, want [{llm error} {rules ok}]", hops)
	}
}

func TestChainInvalidResponseFallsThroughToNextRung(t *testing.T) {
	for name, bad := range invalidResponses() {
		t.Run(name, func(t *testing.T) {
			badRung := &fakeRung{name: "llm", resp: bad}
			rules := &fakeRung{name: RulesName, resp: validResponse()}
			c, err := NewChain([]string{"llm", RulesName}, map[string]Provider{"llm": badRung, RulesName: rules}, testConfig(), logger.Discard())
			if err != nil {
				t.Fatalf("NewChain: %v", err)
			}

			resp, err := c.Classify(context.Background(), &classifierv1.ClassifyRequest{})
			if err != nil {
				t.Fatalf("Classify: %v", err)
			}
			hops := resp.GetHops()
			if len(hops) != 2 || hops[0].GetResult() != "schema_invalid" {
				t.Errorf("hops = %+v, want first hop schema_invalid", hops)
			}
			if rules.calls != 1 {
				t.Error("next rung was not tried after a schema_invalid response")
			}
		})
	}
}

func TestChainStopsAtFirstSuccessfulRung(t *testing.T) {
	first := &fakeRung{name: "llm", resp: validResponse()}
	second := &fakeRung{name: RulesName, resp: validResponse()}
	c, err := NewChain([]string{"llm", RulesName}, map[string]Provider{"llm": first, RulesName: second}, testConfig(), logger.Discard())
	if err != nil {
		t.Fatalf("NewChain: %v", err)
	}

	resp, err := c.Classify(context.Background(), &classifierv1.ClassifyRequest{})
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if second.calls != 0 {
		t.Errorf("later rung was invoked after an earlier one already answered: calls = %d", second.calls)
	}
	if len(resp.GetHops()) != 1 {
		t.Errorf("hops = %+v, want exactly one hop", resp.GetHops())
	}
}

func TestChainAlwaysTerminatesWhenRulesIsLast(t *testing.T) {
	failingA := &fakeRung{name: "a", err: errors.New("boom")}
	failingB := &fakeRung{name: "b", resp: invalidResponses()["bucket outside enum"]}
	rules := &fakeRung{name: RulesName, resp: validResponse()}
	c, err := NewChain([]string{"a", "b", RulesName}, map[string]Provider{"a": failingA, "b": failingB, RulesName: rules}, testConfig(), logger.Discard())
	if err != nil {
		t.Fatalf("NewChain: %v", err)
	}

	resp, err := c.Classify(context.Background(), &classifierv1.ClassifyRequest{})
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if resp.GetSource() != commonv1.Source_SOURCE_RULES_FALLBACK {
		t.Errorf("Source = %v, want SOURCE_RULES_FALLBACK", resp.GetSource())
	}
}

func TestChainForceRulesOnlySkipsNonRulesRungs(t *testing.T) {
	llm := &fakeRung{name: "llm", resp: validResponse()}
	rules := &fakeRung{name: RulesName, resp: validResponse()}
	c, err := NewChain([]string{"llm", RulesName}, map[string]Provider{"llm": llm, RulesName: rules}, testConfig(), logger.Discard())
	if err != nil {
		t.Fatalf("NewChain: %v", err)
	}

	_, err = c.Classify(context.Background(), &classifierv1.ClassifyRequest{ForceRulesOnly: true})
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if llm.calls != 0 {
		t.Errorf("non-rules rung was invoked with force_rules_only set: calls = %d", llm.calls)
	}
	if rules.calls != 1 {
		t.Errorf("rules rung calls = %d, want 1", rules.calls)
	}
}

func TestNewChainRejectsUnknownProviderName(t *testing.T) {
	registry := map[string]Provider{RulesName: &fakeRung{name: RulesName, resp: validResponse()}}
	if _, err := NewChain([]string{"nonexistent"}, registry, testConfig(), logger.Discard()); err == nil {
		t.Fatal("NewChain with unknown provider name: want error, got nil")
	}
}
