package provider

import (
	"context"
	"testing"
	"time"
)

func TestRungCtxWithoutInboundDeadlineAppliesThePerRungCap(t *testing.T) {
	ctx, cancel, ok := rungCtx(context.Background(), 2*time.Second, 150*time.Millisecond, false)
	defer cancel()
	if !ok {
		t.Fatal("ok = false, want true: there is no inbound deadline to run out of")
	}
	deadline, has := ctx.Deadline()
	if !has {
		t.Fatal("rung context has no deadline, want the per-rung cap applied")
	}
	if got := time.Until(deadline); got > 2*time.Second {
		t.Errorf("rung budget = %s, want at most the 2s per-rung cap", got)
	}
}

func TestRungCtxCapsAtThePerRungTimeoutWhenTheDeadlineIsGenerous(t *testing.T) {
	parent, pcancel := context.WithTimeout(context.Background(), time.Minute)
	defer pcancel()

	ctx, cancel, ok := rungCtx(parent, 2*time.Second, 150*time.Millisecond, false)
	defer cancel()
	if !ok {
		t.Fatal("ok = false, want true")
	}
	deadline, _ := ctx.Deadline()
	// A minute of headroom must not become a minute-long provider call.
	if got := time.Until(deadline); got > 2*time.Second {
		t.Errorf("rung budget = %s, want it capped at the 2s per-rung timeout", got)
	}
}

func TestRungCtxCapsAtTheRemainingDeadlineWhenItIsTighterThanThePerRungTimeout(t *testing.T) {
	parent, pcancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer pcancel()

	ctx, cancel, ok := rungCtx(parent, 2*time.Second, 150*time.Millisecond, false)
	defer cancel()
	if !ok {
		t.Fatal("ok = false, want true: 500ms minus a 150ms reserve is still affordable")
	}
	deadline, _ := ctx.Deadline()
	got := time.Until(deadline)
	// 500ms remaining minus the 150ms reserve is ~350ms, well under the 2s cap.
	if got > 360*time.Millisecond {
		t.Errorf("rung budget = %s, want roughly the remaining deadline minus the reserve", got)
	}
	if got <= 0 {
		t.Errorf("rung budget = %s, want positive", got)
	}
}

func TestRungCtxRefusesARungItCannotAfford(t *testing.T) {
	// Less deadline left than the reserve: spending it on a provider call
	// would leave nothing for the rung that always answers.
	parent, pcancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer pcancel()

	_, cancel, ok := rungCtx(parent, 2*time.Second, 150*time.Millisecond, false)
	defer cancel()
	if ok {
		t.Error("ok = true, want false: only 50ms remained against a 150ms reserve")
	}
}

func TestRungCtxAlwaysRunsTheTerminalRung(t *testing.T) {
	// Deadline already blown. The terminal rung is the rules engine: it does
	// no I/O, costs microseconds, and refusing to run it guarantees the
	// failure that running it might still avoid.
	parent, pcancel := context.WithTimeout(context.Background(), -time.Second)
	defer pcancel()

	ctx, cancel, ok := rungCtx(parent, 2*time.Second, 150*time.Millisecond, true)
	defer cancel()
	if !ok {
		t.Error("ok = false for the terminal rung, want true even past the deadline")
	}
	if ctx != parent {
		t.Error("terminal rung got a derived context, want the parent unchanged: it is exempt from the cap")
	}
}
