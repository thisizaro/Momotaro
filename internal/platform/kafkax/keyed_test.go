package kafkax

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

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

// The offsets handed to Kafka must never go backwards. A lower offset landing
// after a higher one moves the group's committed offset back and redelivers
// everything in between, which is what happened when each worker committed
// its own record concurrently (docs/INCIDENTS.md 2026-08-23).
func TestRunCommitterNeverCommitsBackwards(t *testing.T) {
	tracker := newCommitTracker()
	const n = 10
	// observe happens in fetch order, as ConsumeKeyed does it.
	for off := int64(0); off < n; off++ {
		tracker.observe(rec(0, off))
	}

	// Records finish in a scrambled order, the way a worker pool really
	// completes them.
	completed := make(chan *kgo.Record, n)
	for _, off := range []int64{1, 0, 3, 2, 5, 4, 9, 6, 8, 7} {
		completed <- rec(0, off)
	}
	close(completed)

	var got []int64
	commit := func(ctx context.Context, r *kgo.Record) error {
		got = append(got, r.Offset)
		return nil
	}
	runCommitter(context.Background(), tracker, completed, commit,
		func(err error) { t.Errorf("unexpected failure: %v", err) })

	if len(got) == 0 {
		t.Fatal("no commits issued at all")
	}
	for i := 1; i < len(got); i++ {
		if got[i] <= got[i-1] {
			t.Errorf("commit %d went backwards or repeated: sequence %v", i, got)
		}
	}
	if last := got[len(got)-1]; last != n-1 {
		t.Errorf("final committed offset = %d, want %d: the last records were never committed, so they would be redelivered", last, n-1)
	}
}

// A commit failure must not strand the workers. If the committer returned on
// error, a worker blocked sending to a channel nobody reads would never
// finish and ConsumeKeyed's wg.Wait would deadlock behind it.
func TestRunCommitterKeepsDrainingAfterACommitFailure(t *testing.T) {
	tracker := newCommitTracker()
	for off := int64(0); off < 4; off++ {
		tracker.observe(rec(0, off))
	}
	completed := make(chan *kgo.Record, 4)
	for off := int64(0); off < 4; off++ {
		completed <- rec(0, off)
	}
	close(completed)

	var failures int
	calls := 0
	commit := func(ctx context.Context, r *kgo.Record) error {
		calls++
		return errors.New("broker unavailable")
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		runCommitter(context.Background(), tracker, completed, commit, func(error) { failures++ })
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("runCommitter did not drain the channel after a commit failure")
	}

	if failures != 1 {
		t.Errorf("failures reported = %d, want 1: only the first should escalate", failures)
	}
	if calls != 1 {
		t.Errorf("commit attempts = %d, want 1: it must stop committing but keep draining", calls)
	}
}
