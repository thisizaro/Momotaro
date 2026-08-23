package kafkax

import (
	"context"
	"fmt"
	"hash/fnv"
	"sync"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
)

// commitTimeout bounds each individual offset commit. Deliberately its own
// context rather than borrowing ctx's deadline: see ConsumeKeyed's doc
// comment for why a commit must be able to outlive the cancellation that
// triggers it.
const commitTimeout = 10 * time.Second

// commitTracker computes, per partition, the highest contiguous offset
// completed so far. A fetched-but-unfinished record at offset N must pin
// the commit point at N-1 no matter how many later offsets have already
// finished (docs/ARCHITECTURE.md section 8a); naively committing whatever
// finished most recently would silently skip records still in flight
// behind it if the pod crashes.
//
// observe must be called once per record, in fetch order, before that
// record is handed to a worker: it is what establishes the true starting
// offset per partition. complete is called by whichever worker finishes
// that record, in whatever order they actually finish.
type commitTracker struct {
	mu        sync.Mutex
	next      map[int32]int64
	completed map[int32]map[int64]*kgo.Record
}

func newCommitTracker() *commitTracker {
	return &commitTracker{
		next:      make(map[int32]int64),
		completed: make(map[int32]map[int64]*kgo.Record),
	}
}

func (t *commitTracker) observe(rec *kgo.Record) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.next[rec.Partition]; !ok {
		t.next[rec.Partition] = rec.Offset
		t.completed[rec.Partition] = make(map[int64]*kgo.Record)
	}
}

// complete marks rec's offset done and returns the highest-offset record
// now safe to commit for its partition, if this completion advanced the
// contiguous prefix. Safe to call more than once for the same offset (a
// no-op the second time).
func (t *commitTracker) complete(rec *kgo.Record) (*kgo.Record, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	partition := rec.Partition
	next := t.next[partition]
	if rec.Offset < next {
		return nil, false // already folded into an earlier commit
	}
	t.completed[partition][rec.Offset] = rec

	var advanced *kgo.Record
	for {
		r, ok := t.completed[partition][next]
		if !ok {
			break
		}
		delete(t.completed[partition], next)
		advanced = r
		next++
	}
	t.next[partition] = next
	return advanced, advanced != nil
}

// workerFor hashes key to a worker index in [0, poolSize), so every record
// for the same key always lands on the same worker and stays strictly
// ordered relative to itself, while different keys process concurrently
// (docs/ARCHITECTURE.md section 8a).
func workerFor(key string, poolSize int) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return int(h.Sum32()) % poolSize
}

type dispatched struct {
	rec *kgo.Record
	msg Message
}

// ConsumeKeyed processes fetched records across a bounded pool of workers,
// dispatched by key so ordering is preserved per key while different keys
// process in parallel. This is the fix for the throughput ceiling a plain
// consume-one-then-process loop hits once a handler makes a blocking
// downstream call (docs/ARCHITECTURE.md section 8a): a single record
// waiting seconds on an LLM call no longer blocks every other record behind
// it in the fetch.
//
// handler must resolve every record's fate itself, whether that means
// success or a business failure routed to a dead-letter queue: returning
// nil tells ConsumeKeyed the record is done and safe to fold into the next
// commit. A non-nil error is fatal, the same contract as Consume, and is
// meant for infrastructure failures, not per-record business outcomes; it
// stops the whole loop.
//
// Cancelling ctx stops the fetch loop and every handler call, but commits
// for work already dispatched before that point are issued on a short-lived
// context of their own rather than ctx itself: otherwise the exact signal
// that starts a graceful shutdown would also be the signal that aborts the
// final commits that shutdown depends on to not lose progress.
//
// Returns when ctx is cancelled, or when a fetch/handle/commit error
// occurs.
func (c *Consumer) ConsumeKeyed(ctx context.Context, poolSize int, handler func(context.Context, Message) error) error {
	if poolSize <= 0 {
		return fmt.Errorf("pool size must be positive, got %d", poolSize)
	}

	workCtx, cancelWork := context.WithCancel(ctx)
	defer cancelWork()

	tracker := newCommitTracker()
	channels := make([]chan dispatched, poolSize)
	for i := range channels {
		channels[i] = make(chan dispatched)
	}

	var wg sync.WaitGroup
	var failOnce sync.Once
	var failErr error
	fail := func(err error) {
		failOnce.Do(func() { failErr = err })
		cancelWork()
	}

	// completed carries finished records to the single committer goroutine.
	// Buffered so a worker hands off and goes straight back to processing
	// rather than waiting on the commit's network round trip.
	completed := make(chan *kgo.Record, poolSize)

	for i := 0; i < poolSize; i++ {
		wg.Add(1)
		go func(ch <-chan dispatched) {
			defer wg.Done()
			for d := range ch {
				if err := handler(workCtx, d.msg); err != nil {
					fail(fmt.Errorf("handle %s[%d]@%d: %w", d.rec.Topic, d.rec.Partition, d.rec.Offset, err))
					continue
				}
				completed <- d.rec
			}
		}(channels[i])
	}

	var committer sync.WaitGroup
	committer.Add(1)
	go func() {
		defer committer.Done()
		runCommitter(ctx, tracker, completed, func(ctx context.Context, rec *kgo.Record) error {
			return c.cl.CommitRecords(ctx, rec)
		}, fail)
	}()

	fetchErr := c.fetchAndDispatch(workCtx, tracker, channels)

	for _, ch := range channels {
		close(ch)
	}
	// Order matters: every worker must be finished before completed is
	// closed, or a worker still holding a record would send on a closed
	// channel. Then the committer drains what is left and exits, which is
	// what makes every commit for already-finished work land before this
	// function returns.
	wg.Wait()
	close(completed)
	committer.Wait()

	if failErr != nil {
		return failErr
	}
	return fetchErr
}

// commitFunc issues one offset commit. Named so runCommitter's ordering
// contract can be tested without a broker.
type commitFunc func(ctx context.Context, rec *kgo.Record) error

// runCommitter folds finished records into offset commits, strictly one at a
// time, and returns once completed is closed and drained.
//
// One committer rather than a commit per worker is load-bearing, not tidiness.
// Kafka offset commits are last-write-wins per partition, so two workers
// committing concurrently can land a lower offset after a higher one, which
// moves the group's committed offset BACKWARDS and redelivers everything in
// between. commitTracker already hands out strictly increasing offsets, but
// that guarantee is worth nothing if the commits it hands out are then issued
// in parallel, which is exactly the bug this replaced
// (docs/INCIDENTS.md 2026-08-23).
func runCommitter(ctx context.Context, tracker *commitTracker, completed <-chan *kgo.Record, commit commitFunc, fail func(error)) {
	broken := false
	for rec := range completed {
		// Folded in even after a failure, so the tracker stays a truthful
		// account of what actually finished.
		commitRec, ok := tracker.complete(rec)
		if !ok || broken {
			continue
		}
		// Deliberately not ctx: a shutdown cancels it the instant it is
		// requested, which would abort this exact commit for work that has
		// already finished. commitCtx outlives that cancellation so
		// completed work is never lost to the signal that triggered the
		// shutdown.
		commitCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), commitTimeout)
		err := commit(commitCtx, commitRec)
		cancel()
		if err != nil {
			fail(fmt.Errorf("commit %s[%d]@%d: %w", commitRec.Topic, commitRec.Partition, commitRec.Offset, err))
			// Keep draining rather than returning: a worker blocked sending
			// to a channel nobody reads would never finish, and ConsumeKeyed's
			// wg.Wait would deadlock behind it.
			broken = true
		}
	}
}

// fetchAndDispatch is ConsumeKeyed's main loop: poll, register each record
// with tracker (in fetch order, before it can reach any worker), then
// dispatch it to its key's worker channel.
func (c *Consumer) fetchAndDispatch(ctx context.Context, tracker *commitTracker, channels []chan dispatched) error {
	poolSize := len(channels)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		fetches := c.cl.PollFetches(ctx)

		if err := ctx.Err(); err != nil {
			return err
		}

		var fetchErr error
		fetches.EachError(func(topic string, partition int32, err error) {
			if fetchErr == nil {
				fetchErr = fmt.Errorf("fetch %s[%d]: %w", topic, partition, err)
			}
		})
		if fetchErr != nil {
			return fetchErr
		}

		iter := fetches.RecordIter()
		for !iter.Done() {
			rec := iter.Next()
			tracker.observe(rec)

			d := dispatched{rec: rec, msg: Message{
				Topic:     rec.Topic,
				Partition: rec.Partition,
				Offset:    rec.Offset,
				Key:       string(rec.Key),
				Value:     rec.Value,
			}}

			select {
			case channels[workerFor(d.msg.Key, poolSize)] <- d:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}
