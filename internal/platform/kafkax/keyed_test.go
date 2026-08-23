package kafkax

import (
	"fmt"
	"testing"

	"github.com/twmb/franz-go/pkg/kgo"
)

func rec(partition int32, offset int64) *kgo.Record {
	return &kgo.Record{Partition: partition, Offset: offset}
}

func TestCommitTrackerAdvancesInOrder(t *testing.T) {
	tr := newCommitTracker()
	tr.observe(rec(0, 10))
	tr.observe(rec(0, 11))
	tr.observe(rec(0, 12))

	if got, ok := tr.complete(rec(0, 10)); !ok || got.Offset != 10 {
		t.Fatalf("complete(10) = (%v, %v), want (offset 10, true)", got, ok)
	}
	if got, ok := tr.complete(rec(0, 11)); !ok || got.Offset != 11 {
		t.Fatalf("complete(11) = (%v, %v), want (offset 11, true)", got, ok)
	}
	if got, ok := tr.complete(rec(0, 12)); !ok || got.Offset != 12 {
		t.Fatalf("complete(12) = (%v, %v), want (offset 12, true)", got, ok)
	}
}

// This is THE property docs/ARCHITECTURE.md section 8a exists to guarantee:
// an unfinished record at offset N must pin the commit point at N-1 no
// matter how many later offsets have already finished.
func TestCommitTrackerWithholdsCommitUntilGapFills(t *testing.T) {
	tr := newCommitTracker()
	tr.observe(rec(0, 10))
	tr.observe(rec(0, 11))
	tr.observe(rec(0, 12))

	// 12 finishes first; nothing is safe to commit yet, 10 and 11 are still
	// outstanding.
	if _, ok := tr.complete(rec(0, 12)); ok {
		t.Fatal("complete(12) advanced the commit point before 10 and 11 finished")
	}

	// 10 finishes: only 10 itself is now contiguous (11 is still missing).
	got, ok := tr.complete(rec(0, 10))
	if !ok || got.Offset != 10 {
		t.Fatalf("complete(10) = (%v, %v), want (offset 10, true)", got, ok)
	}

	// 11 finishes: this closes the gap, so the whole run up to 12 commits
	// in one step.
	got, ok = tr.complete(rec(0, 11))
	if !ok || got.Offset != 12 {
		t.Fatalf("complete(11) = (%v, %v), want (offset 12, true): finishing 11 should fold in the already-completed 12", got, ok)
	}
}

func TestCommitTrackerPartitionsAreIndependent(t *testing.T) {
	tr := newCommitTracker()
	tr.observe(rec(0, 5))
	tr.observe(rec(1, 100))

	// Completing partition 1 must not be blocked by, or advance, partition 0.
	got, ok := tr.complete(rec(1, 100))
	if !ok || got.Offset != 100 || got.Partition != 1 {
		t.Fatalf("complete(partition 1, 100) = (%v, %v), want (partition 1 offset 100, true)", got, ok)
	}

	if _, ok := tr.complete(rec(0, 5)); !ok {
		t.Fatal("complete(partition 0, 5) did not advance despite being partition 0's first offset")
	}
}

func TestCommitTrackerDuplicateCompleteIsSafe(t *testing.T) {
	tr := newCommitTracker()
	tr.observe(rec(0, 1))

	if _, ok := tr.complete(rec(0, 1)); !ok {
		t.Fatal("first complete(1) should advance")
	}
	// A redelivered/duplicate completion for an already-committed offset
	// must not panic or wrongly re-advance.
	if got, ok := tr.complete(rec(0, 1)); ok {
		t.Errorf("duplicate complete(1) advanced again: got %v", got)
	}
}

// workerFor is the whole basis of ConsumeKeyed's ordering guarantee: same
// key means same worker means per-key order preserved. It had no unit test
// until a flaky integration test made its absence obvious
// (docs/INCIDENTS.md).

func TestWorkerForIsStableForTheSameKey(t *testing.T) {
	const poolSize = 8
	first := workerFor("record-abc", poolSize)
	for i := 0; i < 100; i++ {
		if got := workerFor("record-abc", poolSize); got != first {
			t.Fatalf("workerFor drifted on call %d: got %d, want %d; per-key ordering depends on this never varying", i, got, first)
		}
	}
}

func TestWorkerForStaysInRange(t *testing.T) {
	for _, poolSize := range []int{1, 2, 4, 8, 32} {
		for i := 0; i < 500; i++ {
			key := fmt.Sprintf("record-%d", i)
			got := workerFor(key, poolSize)
			if got < 0 || got >= poolSize {
				t.Fatalf("workerFor(%q, %d) = %d, outside [0,%d): a negative or oversized index would panic on the worker slice", key, poolSize, got, poolSize)
			}
		}
	}
}

// Guards against a degenerate hash that routes everything to worker 0, which
// would still pass the two tests above while silently serialising the entire
// pool.
func TestWorkerForSpreadsAcrossThePool(t *testing.T) {
	const poolSize = 4
	seen := make(map[int]bool)
	for i := 0; i < 200; i++ {
		seen[workerFor(fmt.Sprintf("record-%d", i), poolSize)] = true
	}
	if len(seen) != poolSize {
		t.Errorf("200 distinct keys reached only %d of %d workers: %v", len(seen), poolSize, seen)
	}
}
