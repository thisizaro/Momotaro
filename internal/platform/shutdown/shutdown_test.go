package shutdown

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunWaitsForCancellationThenRunsClosers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	var called int32
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, time.Second, func(ctx context.Context) error {
			atomic.AddInt32(&called, 1)
			return nil
		})
	}()

	select {
	case <-done:
		t.Fatal("Run returned before ctx was cancelled")
	case <-time.After(20 * time.Millisecond):
	}

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return after ctx was cancelled")
	}

	if got := atomic.LoadInt32(&called); got != 1 {
		t.Errorf("closer called %d times, want 1", got)
	}
}

func TestCloseRunsAllClosersConcurrently(t *testing.T) {
	start := make(chan struct{})
	release := make(chan struct{})
	var reached int32

	slow := func(ctx context.Context) error {
		atomic.AddInt32(&reached, 1)
		select {
		case <-start:
		default:
			close(start)
		}
		<-release
		return nil
	}

	fast := func(ctx context.Context) error {
		// Wait for the slow closer to prove it is actually running
		// concurrently, then let both finish.
		select {
		case <-start:
		case <-time.After(time.Second):
		}
		close(release)
		return nil
	}

	errCh := make(chan error, 1)
	go func() { errCh <- Close(time.Second, slow, fast) }()

	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("Close returned %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not return; closers may be running sequentially and deadlocking")
	}

	if got := atomic.LoadInt32(&reached); got != 1 {
		t.Errorf("slow closer reached %d times, want 1", got)
	}
}

func TestCloseJoinsErrors(t *testing.T) {
	e1 := errors.New("first closer failed")
	e2 := errors.New("second closer failed")

	err := Close(time.Second,
		func(ctx context.Context) error { return e1 },
		func(ctx context.Context) error { return nil },
		func(ctx context.Context) error { return e2 },
	)
	if err == nil {
		t.Fatal("expected a joined error")
	}
	if !errors.Is(err, e1) {
		t.Errorf("joined error does not wrap %v: %v", e1, err)
	}
	if !errors.Is(err, e2) {
		t.Errorf("joined error does not wrap %v: %v", e2, err)
	}
}

// A closer that ignores its context and hangs must not block shutdown
// forever: Kubernetes will SIGKILL after its own grace period regardless,
// so Close must return once ours elapses rather than leaking the goroutine
// wait indefinitely.
func TestCloseReturnsAfterGraceEvenIfACloserHangs(t *testing.T) {
	hang := make(chan struct{}) // never closed

	errCh := make(chan error, 1)
	go func() {
		errCh <- Close(20*time.Millisecond, func(ctx context.Context) error {
			<-hang
			return nil
		})
	}()

	select {
	case err := <-errCh:
		if err == nil {
			t.Error("expected an error reporting the exceeded grace period")
		}
	case <-time.After(time.Second):
		t.Fatal("Close blocked past the grace period on a hung closer")
	}
}

// Each closer receives a context so a well-behaved one (e.g. grpc's
// GracefulStop wrapped to respect ctx, or a Kafka consumer's final commit)
// can bail out early instead of running the full grace period every time.
func TestClosersReceiveAContextBoundedByGrace(t *testing.T) {
	var deadlineSet bool
	err := Close(50*time.Millisecond, func(ctx context.Context) error {
		_, deadlineSet = ctx.Deadline()
		return nil
	})
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !deadlineSet {
		t.Error("closer's context had no deadline")
	}
}

func TestCloseWithNoClosersReturnsImmediately(t *testing.T) {
	done := make(chan error, 1)
	go func() { done <- Close(time.Second) }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Close() = %v, want nil", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Close with no closers did not return promptly")
	}
}
