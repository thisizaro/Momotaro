// Package clock provides an injectable time source.
//
// Every service takes a Clock rather than calling time.Now() directly. This
// is not a style preference: Momotaro's core behaviours are time-based
// (salary-window retry scheduling, cooldown expiry, due_at claiming, delayed
// outcomes) and every one of them is untestable if the code reaches for the
// wall clock. See docs/ENGINEERING.md section 2.
//
// With a Fake you can assert "an insufficient-funds failure on the 28th
// schedules a retry for the 1st" in microseconds. Without one you cannot
// assert it at all.
package clock

import (
	"sync"
	"time"
)

// Clock is a source of time. Production uses Real, tests use Fake.
type Clock interface {
	// Now returns the current time.
	Now() time.Time
	// Since is shorthand for Now().Sub(t).
	Since(t time.Time) time.Duration
	// After returns a channel that receives once d has elapsed. Use this
	// rather than time.After so tests can drive it.
	After(d time.Duration) <-chan time.Time
	// Sleep blocks for d. In a Fake it returns once the fake time has been
	// advanced far enough.
	Sleep(d time.Duration)
}

// Real is the production clock, backed by the wall clock.
type Real struct{}

// New returns the production clock.
func New() Clock { return Real{} }

func (Real) Now() time.Time                         { return time.Now() }
func (Real) Since(t time.Time) time.Duration        { return time.Since(t) }
func (Real) After(d time.Duration) <-chan time.Time { return time.After(d) }
func (Real) Sleep(d time.Duration)                  { time.Sleep(d) }

// Fake is a controllable clock for tests. Time only moves when you move it.
//
// Safe for concurrent use, which matters because the code under test often
// runs a worker pool.
type Fake struct {
	mu      sync.Mutex
	now     time.Time
	waiters []waiter
}

type waiter struct {
	at time.Time
	ch chan time.Time
}

// NewFake returns a Fake started at the given time. Pass a fixed, meaningful
// instant rather than time.Now(), or the test stops being deterministic.
func NewFake(start time.Time) *Fake {
	return &Fake{now: start}
}

func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *Fake) Since(t time.Time) time.Duration { return f.Now().Sub(t) }

func (f *Fake) After(d time.Duration) <-chan time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch := make(chan time.Time, 1)
	if d <= 0 {
		ch <- f.now
		return ch
	}
	f.waiters = append(f.waiters, waiter{at: f.now.Add(d), ch: ch})
	return ch
}

// Sleep blocks until the fake clock has been advanced by at least d.
//
// Deadlocks if nothing else advances the clock, which is the correct
// behaviour: a test that sleeps without advancing time has a bug.
func (f *Fake) Sleep(d time.Duration) {
	<-f.After(d)
}

// Advance moves time forward and fires any waiters that are now due.
func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	f.now = f.now.Add(d)
	var due []waiter
	remaining := f.waiters[:0]
	for _, w := range f.waiters {
		if !w.at.After(f.now) {
			due = append(due, w)
		} else {
			remaining = append(remaining, w)
		}
	}
	f.waiters = remaining
	now := f.now
	f.mu.Unlock()

	// Fire outside the lock so a waiter that re-enters the clock cannot
	// deadlock against us.
	for _, w := range due {
		w.ch <- now
	}
}

// Set moves the clock to an absolute time, firing anything now due. Useful
// for "it is now the 1st of the month" style tests.
func (f *Fake) Set(t time.Time) {
	f.mu.Lock()
	d := t.Sub(f.now)
	f.mu.Unlock()
	if d > 0 {
		f.Advance(d)
		return
	}
	f.mu.Lock()
	f.now = t
	f.mu.Unlock()
}
