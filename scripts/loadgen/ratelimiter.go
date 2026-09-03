package main

import (
	"context"
	"fmt"
	"time"

	"github.com/thisizaro/Momotaro/internal/platform/clock"
)

// RateLimiter paces a series of sends to a fixed events-per-second rate,
// steadily rather than in bursts. Each event's send time is computed as a
// fixed offset from the run's start (NextSendTime), never accumulated from
// the previous send, so a slow send (a slow HTTP round trip, GC pause,
// whatever) can only ever fall behind schedule, it can never cause the next
// several events to fire back to back to "catch up".
type RateLimiter struct {
	interval time.Duration
	start    time.Time
	clk      clock.Clock
}

// NewRateLimiter returns a limiter for eventsPerSecond, anchored at start.
// clk is injected per docs/ENGINEERING.md section 2: the send loop's pacing
// is exactly the kind of time-based logic that is untestable against the
// wall clock.
func NewRateLimiter(eventsPerSecond float64, start time.Time, clk clock.Clock) (*RateLimiter, error) {
	if eventsPerSecond <= 0 {
		return nil, fmt.Errorf("rate must be positive, got %v", eventsPerSecond)
	}
	return &RateLimiter{
		interval: time.Duration(float64(time.Second) / eventsPerSecond),
		start:    start,
		clk:      clk,
	}, nil
}

// NextSendTime returns the scheduled time for the nth event (0-indexed).
func (r *RateLimiter) NextSendTime(n int) time.Time {
	return r.start.Add(time.Duration(n) * r.interval)
}

// Wait blocks until NextSendTime(n) or ctx is cancelled, whichever comes
// first, so a caller can be interrupted (SIGINT via a cancelled context)
// instead of always finishing out a whole wait.
func (r *RateLimiter) Wait(ctx context.Context, n int) error {
	d := r.NextSendTime(n).Sub(r.clk.Now())
	if d <= 0 {
		// Already at or past schedule (e.g. the very first event, or one
		// after a slow prior send); proceed immediately rather than
		// blocking on a zero/negative timer.
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}
	select {
	case <-r.clk.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
