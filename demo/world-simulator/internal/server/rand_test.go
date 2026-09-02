package server

import "testing"

// These tests are the core claim of docs/DEMO_READINESS.md Unit AD: two
// runs seeded the same way roll identical outcomes, and two different
// seeds do not. They exercise seededRand and Server.randFor directly,
// with no Postgres/Redis, since neither depends on store or queue.

func TestSeededRandIsDeterministicForTheSameInputs(t *testing.T) {
	a := seededRand{seed: 42, rollKey: "rec-1", attemptNumber: 1}
	b := seededRand{seed: 42, rollKey: "rec-1", attemptNumber: 1}
	if a.Float64() != b.Float64() {
		t.Error("same seed, roll key and attempt_number: want identical draws, got different")
	}
}

func TestSeededRandDrawsDifferForDifferentInputs(t *testing.T) {
	base := seededRand{seed: 42, rollKey: "rec-1", attemptNumber: 1}.Float64()

	if got := (seededRand{seed: 43, rollKey: "rec-1", attemptNumber: 1}).Float64(); got == base {
		t.Error("different seed: want a different draw, got the same")
	}
	if got := (seededRand{seed: 42, rollKey: "rec-2", attemptNumber: 1}).Float64(); got == base {
		t.Error("different roll key: want a different draw, got the same")
	}
	if got := (seededRand{seed: 42, rollKey: "rec-1", attemptNumber: 2}).Float64(); got == base {
		t.Error("different attempt_number: want a different draw, got the same")
	}
}

func TestSeededRandDrawIsInTheUnitInterval(t *testing.T) {
	for i := int64(0); i < 500; i++ {
		f := (seededRand{seed: i, rollKey: "rec", attemptNumber: 1}).Float64()
		if f < 0 || f >= 1 {
			t.Fatalf("seed %d: Float64() = %v, want [0,1)", i, f)
		}
	}
}

// TestSeededRandIsDeterministicUnderConcurrentDraws is the scenario the
// task calls out by name: SimulateOutcome is a gRPC handler, so many
// records' rolls happen concurrently, and a shared sequential stream
// (even mutex-guarded) would make the result depend on goroutine
// scheduling order rather than on the request. Deriving each draw from
// (seed, roll_key, attempt_number) alone means running the same set of
// draws twice, in two different concurrent orders, produces the exact
// same per-record results both times.
func TestSeededRandIsDeterministicUnderConcurrentDraws(t *testing.T) {
	const seed = int64(7)
	recordIDs := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}

	drawAllConcurrently := func() map[string]float64 {
		type result struct {
			recordID string
			v        float64
		}
		out := make(chan result, len(recordIDs))
		for _, id := range recordIDs {
			go func(id string) {
				out <- result{id, (seededRand{seed: seed, rollKey: id, attemptNumber: 3}).Float64()}
			}(id)
		}
		m := make(map[string]float64, len(recordIDs))
		for range recordIDs {
			r := <-out
			m[r.recordID] = r.v
		}
		return m
	}

	run1 := drawAllConcurrently()
	run2 := drawAllConcurrently()
	if len(run1) != len(recordIDs) || len(run2) != len(recordIDs) {
		t.Fatalf("got %d and %d results, want %d each", len(run1), len(run2), len(recordIDs))
	}
	for _, id := range recordIDs {
		if run1[id] != run2[id] {
			t.Errorf("record %s: run1 = %v, run2 = %v, want equal regardless of goroutine order", id, run1[id], run2[id])
		}
	}
}

func TestServerRandForFallsBackToInjectedRngWhenUnseeded(t *testing.T) {
	s := &Server{rng: fixedRand(0.42)}
	if got := s.randFor("rec-1", 1).Float64(); got != 0.42 {
		t.Errorf("randFor with s.seed unset = %v, want 0.42 (the injected s.rng)", got)
	}
}

func TestServerRandForUsesSeededDerivationOnceSeeded(t *testing.T) {
	s := &Server{rng: fixedRand(0.99)}
	s.seed.Store(42)

	want := (seededRand{seed: 42, rollKey: "rec-1", attemptNumber: 1}).Float64()
	if got := s.randFor("rec-1", 1).Float64(); got != want {
		t.Errorf("randFor once seeded = %v, want %v (seededRand's own derivation)", got, want)
	}
}

func TestServerRandForIsDeterministicAcrossRepeatedCallsOnceSeeded(t *testing.T) {
	s := &Server{}
	s.seed.Store(7)
	first := s.randFor("rec-x", 3).Float64()
	second := s.randFor("rec-x", 3).Float64()
	if first != second {
		t.Errorf("randFor(\"rec-x\", 3) called twice = %v then %v, want identical", first, second)
	}
}
