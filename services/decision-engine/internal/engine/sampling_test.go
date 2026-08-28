package engine

import (
	"fmt"
	"testing"
)

func TestSampledForLLMZeroRateSelectsNothing(t *testing.T) {
	for i := 0; i < 200; i++ {
		id := fmt.Sprintf("record-%d", i)
		if sampledForLLM(id, 0.0) {
			t.Fatalf("sampledForLLM(%q, 0.0) = true, want every record excluded at rate 0.0", id)
		}
	}
}

func TestSampledForLLMFullRateSelectsEverything(t *testing.T) {
	for i := 0; i < 200; i++ {
		id := fmt.Sprintf("record-%d", i)
		if !sampledForLLM(id, 1.0) {
			t.Fatalf("sampledForLLM(%q, 1.0) = false, want every record included at rate 1.0", id)
		}
	}
}

// Re-run safety (test/e2e/rerun_safety_test.go) asserts identical outcomes on
// replay. sampledForLLM must therefore be a pure function of (recordID,
// rate): the same record_id takes the same path every time it is called,
// across any number of process restarts, since nothing about it is seeded
// from wall-clock time or process state.
func TestSampledForLLMIsDeterministicAcrossCalls(t *testing.T) {
	const recordID = "8f14e45f-ceea-467e-bd7f-9a13448d132d"
	want := sampledForLLM(recordID, 0.15)
	for i := 0; i < 1000; i++ {
		if got := sampledForLLM(recordID, 0.15); got != want {
			t.Fatalf("sampledForLLM(%q, 0.15) call %d = %v, want %v (same every call)", recordID, i, got, want)
		}
	}
}

// Assert a band, not an exact count: this is a hash distribution over
// synthetic ids, not a fair coin, and pinning an exact figure would break on
// any incidental change to the hash without the sampling actually being
// wrong.
func TestSampledForLLMDistributionWithinBand(t *testing.T) {
	const (
		n    = 10000
		rate = 0.15
		band = 0.02
	)
	selected := 0
	for i := 0; i < n; i++ {
		if sampledForLLM(fmt.Sprintf("record-%d", i), rate) {
			selected++
		}
	}
	got := float64(selected) / n
	if got < rate-band || got > rate+band {
		t.Errorf("rate %.2f selected %d/%d (%.4f), want within +/-%.2f", rate, selected, n, got, band)
	}
}
