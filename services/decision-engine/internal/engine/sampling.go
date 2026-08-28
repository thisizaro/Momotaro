package engine

import "hash/fnv"

// sampledForLLM decides whether recordID gets a live model call this
// classify, or ClassifyRequest.force_rules_only (docs/PHASE3_IMPLEMENTATION.md
// Unit H). rate is LLM_SAMPLE_RATE, validated in [0,1] at startup
// (cmd/main.go).
//
// Deterministic by hash of recordID, not rand.Float64. Re-run safety is a
// headline claim of this project (test/e2e/rerun_safety_test.go asserts
// identical outcomes on replay), and a random draw would make that guarantee
// conditional on a config value: the same record could sample differently
// on a replay, take a different provider path, and legitimately produce a
// different rationale even though nothing about the record changed. Hashing
// costs one FNV pass and removes the possibility entirely: the same
// record_id always takes the same path, on every run, forever.
func sampledForLLM(recordID string, rate float64) bool {
	if rate <= 0 {
		return false
	}
	if rate >= 1 {
		return true
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(recordID)) // fnv.Write never errors
	return h.Sum32()%10000 < uint32(rate*10000)
}
