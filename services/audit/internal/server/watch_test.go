package server

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thisizaro/Momotaro/internal/platform/clock"
	auditv1 "github.com/thisizaro/Momotaro/proto/gen/audit/v1"
)

type fakeChecker struct {
	calls int32
	resp  *auditv1.VerifyInvariantsResponse
	err   error
}

func (f *fakeChecker) VerifyInvariants(ctx context.Context, req *auditv1.VerifyInvariantsRequest) (*auditv1.VerifyInvariantsResponse, error) {
	atomic.AddInt32(&f.calls, 1)
	return f.resp, f.err
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(discardWriter{}, nil))
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

// armReady wires w's beforeWait test hook to a buffered channel, and
// returns a function that blocks until Run has registered its next wait
// with the clock. Run calls clock.After (which synchronously records the
// waiter on a Fake) BEFORE invoking the hook, so once the hook fires,
// advancing the fake clock is guaranteed to land on that exact waiter, not
// race it. This is the channel-synchronisation ENGINEERING.md section 2
// asks for in place of a sleep-and-hope retry loop.
func armReady(w *Watcher) func() {
	ready := make(chan struct{}, 1)
	w.beforeWait = func() { ready <- struct{}{} }
	return func() { <-ready }
}

func TestWatcherChecksOnEveryInterval(t *testing.T) {
	fake := &fakeChecker{resp: &auditv1.VerifyInvariantsResponse{RecordsChecked: 3}}
	fc := clock.NewFake(time.Unix(0, 0))
	w := NewWatcher(fake, fc, time.Minute, discardLogger())
	waitForNextTick := armReady(w)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		w.Run(ctx)
		close(done)
	}()

	for i := 0; i < 3; i++ {
		waitForNextTick()
		fc.Advance(time.Minute)
	}
	waitForNextTick() // proves the 3rd check has completed and a 4th wait began

	if got := atomic.LoadInt32(&fake.calls); got != 3 {
		t.Errorf("checker called %d times, want 3", got)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not return after ctx cancellation")
	}
}

func TestWatcherStopsImmediatelyOnCancelledContext(t *testing.T) {
	fake := &fakeChecker{resp: &auditv1.VerifyInvariantsResponse{}}
	fc := clock.NewFake(time.Unix(0, 0))
	w := NewWatcher(fake, fc, time.Hour, discardLogger())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan struct{})
	go func() {
		w.Run(ctx)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not return promptly for an already-cancelled context")
	}
	if got := atomic.LoadInt32(&fake.calls); got != 0 {
		t.Errorf("checker called %d times before the first interval elapsed", got)
	}
}

func TestWatcherLogsButDoesNotStopOnCheckerError(t *testing.T) {
	fake := &fakeChecker{err: context.DeadlineExceeded}
	fc := clock.NewFake(time.Unix(0, 0))
	w := NewWatcher(fake, fc, time.Minute, discardLogger())
	waitForNextTick := armReady(w)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)

	waitForNextTick()
	fc.Advance(time.Minute)
	waitForNextTick()
	fc.Advance(time.Minute)
	waitForNextTick()

	if got := atomic.LoadInt32(&fake.calls); got != 2 {
		t.Errorf("checker called %d times, want 2 (errors must not stop the loop)", got)
	}
}
