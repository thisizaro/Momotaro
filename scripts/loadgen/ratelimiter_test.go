package main

import (
	"context"
	"testing"
	"time"

	"github.com/thisizaro/Momotaro/internal/platform/clock"
)

func TestNewRateLimiterRejectsNonPositiveRate(t *testing.T) {
	clk := clock.NewFake(time.Unix(0, 0))
	for _, rate := range []float64{0, -1, -0.5} {
		if _, err := NewRateLimiter(rate, clk.Now(), clk); err == nil {
			t.Errorf("rate %v: want error, got nil", rate)
		}
	}
}

// TestNextSendTimeIsEvenlySpaced is the "steady, not bursty" proof: every
// event is pinned to a fixed offset from the run's start time, computed
// fresh each call rather than accumulated from the previous one, so a slow
// send can never compound into acceleration (catching up by bursting) or
// permanent drift (falling further and further behind on top of the
// previous lag).
func TestNextSendTimeIsEvenlySpaced(t *testing.T) {
	start := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	clk := clock.NewFake(start)
	rl, err := NewRateLimiter(10, start, clk) // 10/s -> 100ms apart
	if err != nil {
		t.Fatalf("NewRateLimiter: %v", err)
	}

	want := 100 * time.Millisecond
	prev := rl.NextSendTime(0)
	if !prev.Equal(start) {
		t.Fatalf("NextSendTime(0) = %v, want %v", prev, start)
	}
	for n := 1; n <= 20; n++ {
		next := rl.NextSendTime(n)
		gap := next.Sub(prev)
		if gap != want {
			t.Fatalf("NextSendTime(%d)-NextSendTime(%d) = %v, want exactly %v (no drift, no burst)", n, n-1, gap, want)
		}
		prev = next
	}

	// Explicit spot check further out: offset from start must stay exact,
	// not just consecutive gaps.
	got := rl.NextSendTime(5)
	if want := start.Add(500 * time.Millisecond); !got.Equal(want) {
		t.Fatalf("NextSendTime(5) = %v, want %v", got, want)
	}
}

func TestRateLimiterWaitReturnsImmediatelyWhenTargetAlreadyPassed(t *testing.T) {
	start := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	clk := clock.NewFake(start)
	rl, err := NewRateLimiter(10, start, clk)
	if err != nil {
		t.Fatalf("NewRateLimiter: %v", err)
	}
	// Move the fake clock well past NextSendTime(0), so Wait must not block.
	clk.Advance(time.Second)

	done := make(chan error, 1)
	go func() { done <- rl.Wait(context.Background(), 0) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Wait() = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Wait() blocked when its target time had already passed")
	}
}

func TestRateLimiterWaitReturnsCtxErrOnCancel(t *testing.T) {
	start := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	clk := clock.NewFake(start) // clock has NOT advanced: target is in the future
	rl, err := NewRateLimiter(1, start, clk)
	if err != nil {
		t.Fatalf("NewRateLimiter: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled before Wait is even called

	if err := rl.Wait(ctx, 3); err == nil {
		t.Fatal("Wait() with an already-cancelled context = nil, want an error")
	}
}
