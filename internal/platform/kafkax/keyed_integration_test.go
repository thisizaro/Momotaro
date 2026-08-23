//go:build integration

package kafkax

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

// keysOnDistinctWorkers returns two keys that ConsumeKeyed's own dispatch
// routes to *different* workers in a pool of poolSize, found by asking
// workerFor rather than by hoping.
//
// This exists because the obvious version of this test, two random UUIDs,
// is flaky by construction: dispatch is hash(key) % poolSize, so two
// unrelated keys share a worker with probability 1/poolSize, and a sharing
// pair serialises exactly the thing the test is trying to observe running
// in parallel. See docs/INCIDENTS.md.
func keysOnDistinctWorkers(t *testing.T, poolSize int) (blocked, free string) {
	t.Helper()
	blocked = "keyed-concurrency-blocked"
	for i := 0; i < 256; i++ {
		free = fmt.Sprintf("keyed-concurrency-free-%d", i)
		if workerFor(free, poolSize) != workerFor(blocked, poolSize) {
			return blocked, free
		}
	}
	t.Fatalf("found no key routing to a different worker than %q in a pool of %d", blocked, poolSize)
	return "", ""
}

func TestConsumeKeyedProcessesDifferentKeysConcurrently(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	topic := uniqueTopic(t)
	if err := ensureTopic(ctx, brokers(t), topic); err != nil {
		t.Fatalf("ensureTopic: %v", err)
	}

	producer, err := NewProducer(brokers(t))
	if err != nil {
		t.Fatalf("NewProducer: %v", err)
	}
	defer producer.Close()

	// One constant for both the key choice and ConsumeKeyed below: picking
	// keys for one pool size and then consuming with another is precisely
	// how this test came to be flaky in the first place.
	const poolSize = 4
	blockedKey, freeKey := keysOnDistinctWorkers(t, poolSize)
	if err := producer.Publish(ctx, topic, blockedKey, []byte("blocked")); err != nil {
		t.Fatalf("publish blocked: %v", err)
	}
	if err := producer.Publish(ctx, topic, freeKey, []byte("free")); err != nil {
		t.Fatalf("publish free: %v", err)
	}

	consumer, err := NewConsumer(brokers(t), "kafkax-test-group-"+uuid.NewString(), []string{topic})
	if err != nil {
		t.Fatalf("NewConsumer: %v", err)
	}
	defer consumer.Close()

	release := make(chan struct{})
	freeDone := make(chan struct{})

	consumeCtx, consumeCancel := context.WithCancel(ctx)
	defer consumeCancel()
	go func() {
		_ = consumer.ConsumeKeyed(consumeCtx, poolSize, func(ctx context.Context, m Message) error {
			switch m.Key {
			case blockedKey:
				<-release // held open until the test explicitly frees it
			case freeKey:
				close(freeDone)
			}
			return nil
		})
	}()

	// If a slow key serialized the whole pool, this would never fire while
	// blockedKey's handler is still parked on <-release.
	select {
	case <-freeDone:
	case <-ctx.Done():
		t.Fatal("the free key's handler never ran while the blocked key's handler was stuck; pool did not run concurrently")
	}
	close(release)
}

func TestConsumeKeyedPreservesOrderPerKey(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	topic := uniqueTopic(t)
	if err := ensureTopic(ctx, brokers(t), topic); err != nil {
		t.Fatalf("ensureTopic: %v", err)
	}

	producer, err := NewProducer(brokers(t))
	if err != nil {
		t.Fatalf("NewProducer: %v", err)
	}
	defer producer.Close()

	key := uuid.NewString()
	const n = 20
	for i := 0; i < n; i++ {
		if err := producer.Publish(ctx, topic, key, []byte(fmt.Sprintf("%d", i))); err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
	}

	consumer, err := NewConsumer(brokers(t), "kafkax-test-group-"+uuid.NewString(), []string{topic})
	if err != nil {
		t.Fatalf("NewConsumer: %v", err)
	}
	defer consumer.Close()

	var mu sync.Mutex
	var seen []string
	done := make(chan struct{})

	consumeCtx, consumeCancel := context.WithCancel(ctx)
	defer consumeCancel()
	go func() {
		_ = consumer.ConsumeKeyed(consumeCtx, 8, func(ctx context.Context, m Message) error {
			mu.Lock()
			seen = append(seen, string(m.Value))
			count := len(seen)
			mu.Unlock()
			if count == n {
				close(done)
			}
			return nil
		})
	}()

	select {
	case <-done:
	case <-ctx.Done():
		t.Fatalf("timed out, only saw %d/%d messages", len(seen), n)
	}

	mu.Lock()
	defer mu.Unlock()
	for i, v := range seen {
		if v != fmt.Sprintf("%d", i) {
			t.Fatalf("seen[%d] = %q, want %q: same-key messages arrived out of order", i, v, fmt.Sprintf("%d", i))
		}
	}
}

// The whole point of contiguous-prefix commits: once ConsumeKeyed has
// genuinely finished with every message, closing it and starting a fresh
// consumer in the SAME group must not redeliver any of them.
func TestConsumeKeyedCommitsSoRedeliveryDoesNotHappen(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	topic := uniqueTopic(t)
	if err := ensureTopic(ctx, brokers(t), topic); err != nil {
		t.Fatalf("ensureTopic: %v", err)
	}
	group := "kafkax-test-group-" + uuid.NewString()

	producer, err := NewProducer(brokers(t))
	if err != nil {
		t.Fatalf("NewProducer: %v", err)
	}
	defer producer.Close()

	const n = 10
	for i := 0; i < n; i++ {
		if err := producer.Publish(ctx, topic, uuid.NewString(), []byte(fmt.Sprintf("%d", i))); err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
	}

	func() {
		consumer, err := NewConsumer(brokers(t), group, []string{topic})
		if err != nil {
			t.Fatalf("NewConsumer: %v", err)
		}
		defer consumer.Close()

		var count int32
		allHandled := make(chan struct{})
		var closeOnce sync.Once

		consumeCtx, consumeCancel := context.WithCancel(ctx)
		consumeReturned := make(chan struct{})
		go func() {
			defer close(consumeReturned)
			_ = consumer.ConsumeKeyed(consumeCtx, 4, func(ctx context.Context, m Message) error {
				if atomic.AddInt32(&count, 1) == n {
					closeOnce.Do(func() { close(allHandled) })
				}
				return nil
			})
		}()

		select {
		case <-allHandled:
		case <-ctx.Done():
			t.Fatalf("first pass: timed out, only processed %d/%d", count, n)
		}

		// Every handler has run, but ConsumeKeyed still needs to fold the
		// last few completions into a commit; only its own return (which
		// waits for every worker to finish) proves that actually happened,
		// not just that handlers were called (docs/ENGINEERING.md section 1:
		// no time.Sleep, synchronise on a real signal).
		consumeCancel()
		select {
		case <-consumeReturned:
		case <-ctx.Done():
			t.Fatal("ConsumeKeyed did not return after cancellation")
		}
	}()

	// Fresh consumer, same group: nothing should arrive within a short,
	// bounded wait.
	consumer2, err := NewConsumer(brokers(t), group, []string{topic})
	if err != nil {
		t.Fatalf("NewConsumer (second pass): %v", err)
	}
	defer consumer2.Close()

	redelivered := make(chan Message, 1)
	waitCtx, waitCancel := context.WithTimeout(ctx, 5*time.Second)
	defer waitCancel()
	go func() {
		_ = consumer2.ConsumeKeyed(waitCtx, 4, func(ctx context.Context, m Message) error {
			redelivered <- m
			return nil
		})
	}()

	select {
	case m := <-redelivered:
		t.Fatalf("message redelivered after a clean ConsumeKeyed pass: %+v", m)
	case <-waitCtx.Done():
		// Expected: nothing arrived.
	}
}
