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

	for i := 0; i < poolSize; i++ {
		wg.Add(1)
		go func(ch <-chan dispatched) {
			defer wg.Done()
			for d := range ch {
				if err := handler(workCtx, d.msg); err != nil {
					fail(fmt.Errorf("handle %s[%d]@%d: %w", d.rec.Topic, d.rec.Partition, d.rec.Offset, err))
					continue
				}
				commitRec, ok := tracker.complete(d.rec)
				if !ok {
					continue
				}
				// Deliberately not ctx or workCtx: a shutdown cancels both
				// the instant it's requested, which would abort this exact
				// commit for the record that just finished. commitCtx
				// outlives that cancellation so already-finished work is
				// never lost to the shutdown signal that triggered it.
				commitCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), commitTimeout)
				err := c.cl.CommitRecords(commitCtx, commitRec)
				cancel()
				if err != nil {
					fail(fmt.Errorf("commit %s[%d]@%d: %w", d.rec.Topic, d.rec.Partition, commitRec.Offset, err))
				}
			}
		}(channels[i])
	}

	fetchErr := c.fetchAndDispatch(workCtx, tracker, channels)

	for _, ch := range channels {
		close(ch)
	}
	wg.Wait()

	if failErr != nil {
		return failErr
	}
	return fetchErr
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
