package provider

import (
	"context"
	"errors"
	"testing"

	"github.com/thisizaro/Momotaro/internal/platform/logger"
	classifierv1 "github.com/thisizaro/Momotaro/proto/gen/classifier/v1"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

// fakeNudgeRung is a test-only NudgeProvider, the ComposeNudge equivalent of
// chain_test.go's fakeRung. Real rungs never appear in this package's
// tests: the chain is tested with fakes, never a real call.
type fakeNudgeRung struct {
	name  string
	calls int
	resp  *classifierv1.ComposeNudgeResponse
	err   error
}

func (f *fakeNudgeRung) Name() string { return f.name }

func (f *fakeNudgeRung) ComposeNudge(ctx context.Context, req *classifierv1.ComposeNudgeRequest) (*classifierv1.ComposeNudgeResponse, error) {
	f.calls++
	return f.resp, f.err
}

func TestNudgeChainSingleTemplateRung(t *testing.T) {
	template := &fakeNudgeRung{name: RulesName, resp: validNudgeResponse()}
	c, err := NewNudgeChain([]string{RulesName}, map[string]NudgeProvider{RulesName: template}, testConfig(), logger.Discard())
	if err != nil {
		t.Fatalf("NewNudgeChain: %v", err)
	}

	resp, err := c.ComposeNudge(context.Background(), &classifierv1.ComposeNudgeRequest{MaxChars: 160})
	if err != nil {
		t.Fatalf("ComposeNudge: %v", err)
	}
	if resp.GetSource() != commonv1.Source_SOURCE_RULES_FALLBACK {
		t.Errorf("Source = %v, want SOURCE_RULES_FALLBACK", resp.GetSource())
	}
	hops := resp.GetHops()
	if len(hops) != 1 || hops[0].GetProvider() != RulesName || hops[0].GetResult() != HopOK {
		t.Errorf("hops = %+v, want exactly [{rules ok}]", hops)
	}
}

func TestNudgeChainRungErrorFallsThroughToNextRung(t *testing.T) {
	failing := &fakeNudgeRung{name: "llm", err: errors.New("boom")}
	template := &fakeNudgeRung{name: RulesName, resp: validNudgeResponse()}
	c, err := NewNudgeChain([]string{"llm", RulesName},
		map[string]NudgeProvider{"llm": failing, RulesName: template}, testConfig(), logger.Discard())
	if err != nil {
		t.Fatalf("NewNudgeChain: %v", err)
	}

	resp, err := c.ComposeNudge(context.Background(), &classifierv1.ComposeNudgeRequest{MaxChars: 160})
	if err != nil {
		t.Fatalf("ComposeNudge: %v", err)
	}
	if failing.calls != 1 || template.calls != 1 {
		t.Errorf("calls: failing=%d template=%d, want 1 and 1", failing.calls, template.calls)
	}
	hops := resp.GetHops()
	if len(hops) != 2 || hops[0].GetProvider() != "llm" || hops[0].GetResult() != HopError ||
		hops[1].GetProvider() != RulesName || hops[1].GetResult() != HopOK {
		t.Errorf("hops = %+v, want [{llm error} {rules ok}]", hops)
	}
}

func TestNudgeChainInvalidResponseFallsThroughToNextRung(t *testing.T) {
	// A model that invents its own digit instead of using AmountPlaceholder
	// is exactly the invalid case ARCHITECTURE.md section 5b calls out.
	bad := &fakeNudgeRung{name: "llm", resp: &classifierv1.ComposeNudgeResponse{Message: "Pay Rs 750 now"}}
	template := &fakeNudgeRung{name: RulesName, resp: validNudgeResponse()}
	c, err := NewNudgeChain([]string{"llm", RulesName},
		map[string]NudgeProvider{"llm": bad, RulesName: template}, testConfig(), logger.Discard())
	if err != nil {
		t.Fatalf("NewNudgeChain: %v", err)
	}

	resp, err := c.ComposeNudge(context.Background(), &classifierv1.ComposeNudgeRequest{MaxChars: 160})
	if err != nil {
		t.Fatalf("ComposeNudge: %v", err)
	}
	hops := resp.GetHops()
	if len(hops) != 2 || hops[0].GetResult() != HopSchemaInvalid {
		t.Errorf("hops = %+v, want first hop schema_invalid", hops)
	}
	if template.calls != 1 {
		t.Error("next rung was not tried after a schema_invalid response")
	}
}

func TestNudgeChainStopsAtFirstSuccessfulRung(t *testing.T) {
	first := &fakeNudgeRung{name: "llm", resp: validNudgeResponse()}
	second := &fakeNudgeRung{name: RulesName, resp: validNudgeResponse()}
	c, err := NewNudgeChain([]string{"llm", RulesName},
		map[string]NudgeProvider{"llm": first, RulesName: second}, testConfig(), logger.Discard())
	if err != nil {
		t.Fatalf("NewNudgeChain: %v", err)
	}

	resp, err := c.ComposeNudge(context.Background(), &classifierv1.ComposeNudgeRequest{MaxChars: 160})
	if err != nil {
		t.Fatalf("ComposeNudge: %v", err)
	}
	if second.calls != 0 {
		t.Errorf("later rung was invoked after an earlier one already answered: calls = %d", second.calls)
	}
	if len(resp.GetHops()) != 1 {
		t.Errorf("hops = %+v, want exactly one hop", resp.GetHops())
	}
}

func TestNudgeChainForceTemplateOnlySkipsNonTemplateRungs(t *testing.T) {
	llm := &fakeNudgeRung{name: "llm", resp: validNudgeResponse()}
	template := &fakeNudgeRung{name: RulesName, resp: validNudgeResponse()}
	c, err := NewNudgeChain([]string{"llm", RulesName},
		map[string]NudgeProvider{"llm": llm, RulesName: template}, testConfig(), logger.Discard())
	if err != nil {
		t.Fatalf("NewNudgeChain: %v", err)
	}

	_, err = c.ComposeNudge(context.Background(), &classifierv1.ComposeNudgeRequest{MaxChars: 160, ForceTemplateOnly: true})
	if err != nil {
		t.Fatalf("ComposeNudge: %v", err)
	}
	if llm.calls != 0 {
		t.Errorf("non-template rung was invoked with force_template_only set: calls = %d", llm.calls)
	}
	if template.calls != 1 {
		t.Errorf("template rung calls = %d, want 1", template.calls)
	}
}

// --- Construction invariants: NewNudgeChain shares validateChainOrder and
// resolveRungs with NewChain (chain.go), so this is a thin confirmation that
// the sharing actually wires up, not a re-derivation of chain_invariants_test.go's
// exhaustive cases against the already-proven shared logic. ---

func TestNewNudgeChainRejectsAChainThatCannotTerminate(t *testing.T) {
	llm := &fakeNudgeRung{name: "llm", resp: validNudgeResponse()}
	reg := map[string]NudgeProvider{"llm": llm}
	if _, err := NewNudgeChain([]string{"llm"}, reg, testConfig(), logger.Discard()); err == nil {
		t.Fatal("NewNudgeChain without the template rung: want error, got nil")
	}
}

func TestNewNudgeChainRejectsUnknownProviderName(t *testing.T) {
	reg := map[string]NudgeProvider{RulesName: &fakeNudgeRung{name: RulesName, resp: validNudgeResponse()}}
	if _, err := NewNudgeChain([]string{"nonexistent", RulesName}, reg, testConfig(), logger.Discard()); err == nil {
		t.Fatal("NewNudgeChain with unknown provider name: want error, got nil")
	}
}

func TestNewNudgeChainAcceptsATemplateOnlyChain(t *testing.T) {
	reg := map[string]NudgeProvider{RulesName: &fakeNudgeRung{name: RulesName, resp: validNudgeResponse()}}
	if _, err := NewNudgeChain([]string{RulesName}, reg, testConfig(), logger.Discard()); err != nil {
		t.Fatalf("NewNudgeChain on the template-only default chain: %v", err)
	}
}
