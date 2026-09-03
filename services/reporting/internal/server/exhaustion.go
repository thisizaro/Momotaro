package server

import commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"

// llmQuotaExhaustedResults are the provider hop outcomes that mean a record
// wanted a live model call and did not get one, distinct from a record the
// deterministic rules table was simply confident enough to answer alone
// (docs/DEMO_READINESS.md Unit AI):
//
//   - "rate_limited" / "circuit_open": the classifier's own provider chain
//     tried a live rung and Groq's free tier, or its own breaker, said no
//     (services/classifier/internal/provider/provider.go).
//   - "exhausted": the Decision Engine judged the record ambiguous but its
//     own LLM_SAMPLE_RATE ceiling was already spent for this batch, so it
//     never even placed the call (services/decision-engine/internal/engine).
//
// Both are quota exhaustion, just at different rungs of the same pipeline,
// and both still resolved to a real answer from the rules table, never an
// unclassified record.
var llmQuotaExhaustedResults = map[string]bool{
	"rate_limited": true,
	"circuit_open": true,
	"exhausted":    true,
}

// llmQuotaExhausted reports whether hops shows this record's classification
// wanted a live model call it did not get. Pure and unit-testable on its
// own, kept separate from the SQL and hopcodec.Decode plumbing in store.go
// (llmQuotaExhaustedCount) so the actual decision rule has a test that
// needs no database.
func llmQuotaExhausted(hops []*commonv1.ProviderHop) bool {
	for _, h := range hops {
		if llmQuotaExhaustedResults[h.GetResult()] {
			return true
		}
	}
	return false
}
