package server

import (
	"testing"

	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

func hop(provider, result string) *commonv1.ProviderHop {
	return &commonv1.ProviderHop{Provider: provider, Result: result}
}

// A record the rules table answered outright, no live rung ever tried, is
// not exhaustion: it never wanted a model call in the first place.
func TestLLMQuotaExhaustedFalseForRulesOnly(t *testing.T) {
	hops := []*commonv1.ProviderHop{hop("rules", "ok")}
	if llmQuotaExhausted(hops) {
		t.Error("llmQuotaExhausted(rules:ok) = true, want false: confident rules-only answers are not exhaustion")
	}
}

// A record a live rung actually answered is not exhaustion either: the
// model was asked and it answered.
func TestLLMQuotaExhaustedFalseForLiveSuccess(t *testing.T) {
	hops := []*commonv1.ProviderHop{hop("groq", "ok")}
	if llmQuotaExhausted(hops) {
		t.Error("llmQuotaExhausted(groq:ok) = true, want false: a live rung answering is not exhaustion")
	}
}

func TestLLMQuotaExhaustedTrueForRateLimited(t *testing.T) {
	hops := []*commonv1.ProviderHop{hop("groq", "rate_limited"), hop("rules", "ok")}
	if !llmQuotaExhausted(hops) {
		t.Error("llmQuotaExhausted(groq:rate_limited, rules:ok) = false, want true: Groq's free tier said no")
	}
}

func TestLLMQuotaExhaustedTrueForCircuitOpen(t *testing.T) {
	hops := []*commonv1.ProviderHop{hop("groq", "circuit_open"), hop("rules", "ok")}
	if !llmQuotaExhausted(hops) {
		t.Error("llmQuotaExhausted(groq:circuit_open, rules:ok) = false, want true: the breaker was already open")
	}
}

// "exhausted" is the Decision Engine's own hop, appended when an ambiguous
// record's turn came after LLM_SAMPLE_RATE's ceiling was already spent: no
// live rung was even tried, so the only hop is the rules answer plus this
// marker.
func TestLLMQuotaExhaustedTrueForSampleBudgetExhausted(t *testing.T) {
	hops := []*commonv1.ProviderHop{hop("sample_budget", "exhausted"), hop("rules", "ok")}
	if !llmQuotaExhausted(hops) {
		t.Error("llmQuotaExhausted(sample_budget:exhausted, rules:ok) = false, want true: the sampling ceiling was already spent")
	}
}

func TestLLMQuotaExhaustedFalseForEmptyHops(t *testing.T) {
	if llmQuotaExhausted(nil) {
		t.Error("llmQuotaExhausted(nil) = true, want false")
	}
}
