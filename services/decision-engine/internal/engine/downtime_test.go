package engine

import (
	"testing"
	"time"

	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

func activeDowntime() downtimeStatus {
	return downtimeStatus{
		Present:    true,
		DowntimeID: "down_F1Zppa6lcVheSE",
		Method:     "netbanking",
		Instrument: "VIJB",
		Severity:   "high",
		Scheduled:  false,
		Begin:      testNow.Add(-time.Hour),
		End:        nil,
	}
}

func TestDowntimeBlocksRetryWhenActiveAndOngoing(t *testing.T) {
	reason, blocked := downtimeBlocksRetry(activeDowntime(), testNow)
	if !blocked {
		t.Fatal("want blocked, an unresolved downtime with no end is ongoing right now")
	}
	want := "bank downtime active: netbanking VIJB, severity high"
	if reason != want {
		t.Errorf("reason = %q, want %q", reason, want)
	}
}

func TestDowntimeDoesNotBlockWhenNotPresent(t *testing.T) {
	if _, blocked := downtimeBlocksRetry(downtimeStatus{}, testNow); blocked {
		t.Error("want unblocked: no downtime row for this instrument at all")
	}
}

func TestDowntimeDoesNotBlockBeforeItBegins(t *testing.T) {
	d := activeDowntime()
	d.Begin = testNow.Add(time.Hour) // begins in the future
	if _, blocked := downtimeBlocksRetry(d, testNow); blocked {
		t.Error("want unblocked: this downtime has not started yet")
	}
}

// A scheduled downtime carries its own published end. Once that passes, it
// stops blocking on its own, resolved event or not.
func TestScheduledDowntimeStopsBlockingOnceItsEndPasses(t *testing.T) {
	end := testNow.Add(-time.Minute)
	d := activeDowntime()
	d.Scheduled = true
	d.Begin = testNow.Add(-2 * time.Hour)
	d.End = &end

	if _, blocked := downtimeBlocksRetry(d, testNow); blocked {
		t.Error("want unblocked: the scheduled window's own end has already passed")
	}
}

func TestScheduledDowntimeStillBlocksBeforeItsEnd(t *testing.T) {
	end := testNow.Add(time.Hour)
	d := activeDowntime()
	d.Scheduled = true
	d.Begin = testNow.Add(-time.Hour)
	d.End = &end

	if _, blocked := downtimeBlocksRetry(d, testNow); !blocked {
		t.Error("want blocked: still inside the scheduled window")
	}
}

// The safety valve (docs/PHASE5_5_IMPLEMENTATION.md Unit Y): an unplanned
// downtime with no end and no resolved event must not hold a record forever.
func TestUnplannedDowntimeExpiresAfterTheMaxUnresolvedHold(t *testing.T) {
	d := activeDowntime()
	d.Begin = testNow.Add(-DowntimeMaxUnresolvedHold - time.Minute)

	if _, blocked := downtimeBlocksRetry(d, testNow); blocked {
		t.Error("want unblocked: past the max-unresolved-hold safety valve with no resolved event")
	}
}

func TestUnplannedDowntimeStillBlocksInsideTheMaxUnresolvedHold(t *testing.T) {
	d := activeDowntime()
	d.Begin = testNow.Add(-DowntimeMaxUnresolvedHold + time.Minute)

	if _, blocked := downtimeBlocksRetry(d, testNow); !blocked {
		t.Error("want blocked: still inside the safety-valve window")
	}
}

// docs/PHASE5_5_IMPLEMENTATION.md Unit Y: an unrecognised severity value
// must be shown, never crash the guardrail.
func TestDowntimeBlocksRetryHandlesAnUnknownSeverityWithoutCrashing(t *testing.T) {
	d := activeDowntime()
	d.Severity = "low" // not one of the two documented examples ("high"/"medium")

	reason, blocked := downtimeBlocksRetry(d, testNow)
	if !blocked {
		t.Fatal("want blocked regardless of severity spelling")
	}
	if reason != "bank downtime active: netbanking VIJB, severity low" {
		t.Errorf("reason = %q, want the unrecognised severity passed through verbatim", reason)
	}
}

// A card-shaped instrument (issuer/network + type) must work exactly like
// netbanking's single-field shape: downtimeBlocksRetry never assumes one
// instrument shape, it only ever sees the single value the Gateway already
// extracted.
func TestDowntimeBlocksRetryWorksWithACardShapedInstrumentValue(t *testing.T) {
	d := activeDowntime()
	d.Method = "card"
	d.Instrument = "SBIN"

	reason, blocked := downtimeBlocksRetry(d, testNow)
	if !blocked {
		t.Fatal("want blocked")
	}
	if reason != "bank downtime active: card SBIN, severity high" {
		t.Errorf("reason = %q", reason)
	}
}

func TestApplyDowntimeGuardrailBlocksRetryAndMarksItDowntimeHeld(t *testing.T) {
	v := applyGuardrails(freshHistory(), testGuardrails, testNow)
	v = applyDowntimeGuardrail(v, activeDowntime(), testNow)

	if v.allows(commonv1.ActionType_ACTION_TYPE_RETRY) {
		t.Fatal("want RETRY blocked while the downtime is active")
	}
	if !v.retryHeldByDowntime {
		t.Error("want retryHeldByDowntime = true, this is exactly the case scoreAndRoute must defer rather than escalate for")
	}
}

func TestApplyDowntimeGuardrailLeavesNudgesAlone(t *testing.T) {
	v := applyGuardrails(freshHistory(), testGuardrails, testNow)
	v = applyDowntimeGuardrail(v, activeDowntime(), testNow)

	for _, action := range []commonv1.ActionType{
		commonv1.ActionType_ACTION_TYPE_NUDGE_METHOD_UPDATE,
		commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER,
	} {
		if !v.allows(action) {
			t.Errorf("%s blocked by a downtime, want permitted: a bank outage does not stop a customer updating their method", action)
		}
	}
}

// A permanent block (the retry budget is spent) must not be overwritten by
// the downtime's own, temporary reason: resolving the downtime would not
// make this record retryable again, so the audit trail must keep saying why.
func TestApplyDowntimeGuardrailDoesNotOverwriteAPermanentRetryBlock(t *testing.T) {
	h := freshHistory()
	h.Retries = testGuardrails.MaxRetries // budget already spent

	v := applyGuardrails(h, testGuardrails, testNow)
	budgetReason := v.reason(commonv1.ActionType_ACTION_TYPE_RETRY)

	v = applyDowntimeGuardrail(v, activeDowntime(), testNow)

	if v.reason(commonv1.ActionType_ACTION_TYPE_RETRY) != budgetReason {
		t.Errorf("reason = %q, want the original retry-budget-exhausted reason unchanged", v.reason(commonv1.ActionType_ACTION_TYPE_RETRY))
	}
	if v.retryHeldByDowntime {
		t.Error("want retryHeldByDowntime = false: the budget, not the downtime, is why this record cannot retry")
	}
}

func TestApplyDowntimeGuardrailIsANoOpWhenNoDowntimeIsPresent(t *testing.T) {
	v := applyGuardrails(freshHistory(), testGuardrails, testNow)
	v = applyDowntimeGuardrail(v, downtimeStatus{}, testNow)

	if !v.allows(commonv1.ActionType_ACTION_TYPE_RETRY) {
		t.Error("want RETRY still permitted: no downtime row for this instrument")
	}
	if v.retryHeldByDowntime {
		t.Error("want retryHeldByDowntime = false")
	}
}
