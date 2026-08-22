package clock

import (
	"sync"
	"testing"
	"time"
)

// Fixed instant chosen deliberately: the 28th, so salary-window tests read
// naturally. Never time.Now() in a test.
var base = time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)

func TestFakeNowDoesNotMoveOnItsOwn(t *testing.T) {
	c := NewFake(base)
	got := c.Now()
	time.Sleep(2 * time.Millisecond) // real time passes
	if !c.Now().Equal(got) {
		t.Fatalf("fake clock moved without Advance: %v then %v", got, c.Now())
	}
}

func TestFakeAdvance(t *testing.T) {
	tests := []struct {
		name string
		adv  time.Duration
		want time.Time
	}{
		{"zero", 0, base},
		{"an hour", time.Hour, base.Add(time.Hour)},
		{"four days, into the salary window", 4 * 24 * time.Hour, time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := NewFake(base)
			c.Advance(tc.adv)
			if !c.Now().Equal(tc.want) {
				t.Errorf("Now() = %v, want %v", c.Now(), tc.want)
			}
		})
	}
}

func TestFakeAfterFiresOnlyWhenDue(t *testing.T) {
	c := NewFake(base)
	ch := c.After(time.Hour)

	select {
	case <-ch:
		t.Fatal("fired before its deadline")
	default:
	}

	c.Advance(59 * time.Minute)
	select {
	case <-ch:
		t.Fatal("fired one minute early")
	default:
	}

	c.Advance(time.Minute)
	select {
	case got := <-ch:
		if !got.Equal(base.Add(time.Hour)) {
			t.Errorf("fired with %v, want %v", got, base.Add(time.Hour))
		}
	default:
		t.Fatal("did not fire when due")
	}
}

func TestFakeAfterNonPositiveFiresImmediately(t *testing.T) {
	c := NewFake(base)
	for _, d := range []time.Duration{0, -time.Second} {
		select {
		case <-c.After(d):
		default:
			t.Errorf("After(%v) did not fire immediately", d)
		}
	}
}

// A single Advance past several deadlines must fire all of them, not just
// the first. The scheduler worker relies on this when it wakes after a gap.
func TestFakeAdvancePastMultipleWaiters(t *testing.T) {
	c := NewFake(base)
	a, b, never := c.After(time.Minute), c.After(2*time.Minute), c.After(time.Hour)

	c.Advance(5 * time.Minute)

	for i, ch := range []<-chan time.Time{a, b} {
		select {
		case <-ch:
		default:
			t.Errorf("waiter %d did not fire", i)
		}
	}
	select {
	case <-never:
		t.Error("waiter beyond the advance fired anyway")
	default:
	}
}

func TestFakeSetMovesToAbsoluteTime(t *testing.T) {
	c := NewFake(base)
	target := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	ch := c.After(48 * time.Hour) // due 2026-08-30

	c.Set(target)

	if !c.Now().Equal(target) {
		t.Errorf("Now() = %v, want %v", c.Now(), target)
	}
	select {
	case <-ch:
	default:
		t.Error("Set past a deadline did not fire it")
	}
}

func TestFakeSince(t *testing.T) {
	c := NewFake(base)
	c.Advance(90 * time.Second)
	if got := c.Since(base); got != 90*time.Second {
		t.Errorf("Since() = %v, want 90s", got)
	}
}

// The clock is shared across a worker pool, so concurrent use must be safe.
// This test is the reason -race is mandatory in CI.
func TestFakeConcurrentUse(t *testing.T) {
	c := NewFake(base)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); _ = c.Now() }()
		go func() { defer wg.Done(); c.Advance(time.Millisecond) }()
	}
	wg.Wait()
	if got := c.Since(base); got != 50*time.Millisecond {
		t.Errorf("Since() = %v, want 50ms; lost an Advance under concurrency", got)
	}
}

func TestRealClockAdvances(t *testing.T) {
	c := New()
	start := c.Now()
	time.Sleep(2 * time.Millisecond)
	if !c.Now().After(start) {
		t.Error("real clock did not advance")
	}
}
