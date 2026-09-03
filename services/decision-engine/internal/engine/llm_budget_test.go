package engine

import (
	"math"
	"sync"
	"testing"
)

// llmBudget is what replaces the old hash-based sampledForLLM (Phase 3 Unit
// H, deleted with this change): LLM_SAMPLE_RATE is no longer a selector
// that decides WHICH records get a live call, it is a ceiling that bounds
// HOW MANY do, across every record this Engine classifies, confident or
// not (docs/DEMO_READINESS.md Unit AI).

// At rate 0, nothing is ever within budget, matching the old
// sampledForLLM(id, 0.0) contract: every existing deployment that never
// sets LLM_SAMPLE_RATE stays free of outbound calls.
func TestLLMBudgetZeroRateNeverAllows(t *testing.T) {
	b := newLLMBudget(0.0)
	for i := 0; i < 200; i++ {
		if b.consider(true) {
			t.Fatalf("consider(true) call %d = true at rate 0.0, want every call denied", i)
		}
	}
}

// At rate 1, every eligible (ambiguous) record is always within budget.
func TestLLMBudgetFullRateAlwaysAllowsEligible(t *testing.T) {
	b := newLLMBudget(1.0)
	for i := 0; i < 200; i++ {
		if !b.consider(true) {
			t.Fatalf("consider(true) call %d = false at rate 1.0, want every eligible call allowed", i)
		}
	}
}

// A confident record (eligible=false) never spends the budget, whatever the
// rate: routing already decided the rules table answers it.
func TestLLMBudgetNeverSpendsOnIneligibleRecord(t *testing.T) {
	b := newLLMBudget(1.0)
	for i := 0; i < 200; i++ {
		if b.consider(false) {
			t.Fatalf("consider(false) call %d = true, want confident records never to spend the budget", i)
		}
	}
}

// The ceiling is a fraction of every record this Engine sees, not only the
// ambiguous ones: three confident records that never asked for a call still
// count in the denominator, so the very next ambiguous record judges its
// budget against a total of 4, not 1.
func TestLLMBudgetCountsIneligibleRecordsInTheDenominator(t *testing.T) {
	b := newLLMBudget(0.5)
	b.consider(false)
	b.consider(false)
	b.consider(false)
	// total is now 3. One eligible record arrives: (0+1)/4 = 0.25 <= 0.5,
	// within budget.
	if !b.consider(true) {
		t.Fatal("consider(true) after 3 confident records at rate 0.5 = false, want true: (llmCalls+1)/total = 1/4 = 0.25 is within budget")
	}
}

// The ceiling must never be exceeded: at any point, llmCalls/total <= rate.
// Checked as a running invariant across a long, all-eligible sequence
// rather than only at the end, since a bug that transiently overspends and
// self-corrects would pass an end-of-run-only check.
func TestLLMBudgetNeverExceedsRateAtAnyPoint(t *testing.T) {
	const rate = 0.15
	b := newLLMBudget(rate)
	var llmCalls, total int
	for i := 0; i < 10000; i++ {
		total++
		if b.consider(true) {
			llmCalls++
		}
		if float64(llmCalls)/float64(total) > rate+1e-9 {
			t.Fatalf("after %d records: llmCalls/total = %d/%d = %.4f, exceeds rate %.4f", total, llmCalls, total, float64(llmCalls)/float64(total), rate)
		}
	}
}

// Over a long all-eligible run the ceiling should be spent close to fully,
// not left mostly unused: this is the "greedy" half of the invariant above,
// proving the budget is a ceiling (it gets used right up to the rate) and
// not an accidental near-zero throttle.
func TestLLMBudgetConvergesCloseToRate(t *testing.T) {
	const (
		rate = 0.15
		n    = 10000
	)
	b := newLLMBudget(rate)
	llmCalls := 0
	for i := 0; i < n; i++ {
		if b.consider(true) {
			llmCalls++
		}
	}
	got := float64(llmCalls) / float64(n)
	if math.Abs(got-rate) > 0.001 {
		t.Errorf("llmCalls/total = %.4f, want within 0.001 of rate %.4f", got, rate)
	}
}

// Concurrent callers must not be able to jointly overspend the ceiling: run
// under -race to also confirm no data race on the shared counters.
func TestLLMBudgetConcurrentConsidersDoNotOverspend(t *testing.T) {
	const (
		rate    = 0.15
		workers = 32
		perW    = 500
	)
	b := newLLMBudget(rate)
	var wg sync.WaitGroup
	var mu sync.Mutex
	llmCalls, total := 0, 0
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perW; i++ {
				allowed := b.consider(true)
				mu.Lock()
				total++
				if allowed {
					llmCalls++
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	got := float64(llmCalls) / float64(total)
	if got > rate+0.01 {
		t.Errorf("concurrent llmCalls/total = %.4f, exceeds rate %.4f by more than tolerance", got, rate)
	}
}
