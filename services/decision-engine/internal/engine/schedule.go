package engine

import (
	"time"

	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

// retryDueAt computes when a record parked in RETRY_SCHEDULED should first
// become actionable, following the root cause for cause-aware retry timing
// (docs/ARCHITECTURE.md section 5a). Returns nil for buckets that must
// never be retried (HARD_DECLINE, RISK_HOLD). For all other buckets, the
// computed delay is divided by timeScale so the salary-window wait actually
// fires inside a compressed demo run (config.Common.Scale's formula).
//
// recordType and mandateLeadTime exist for one rule (docs/PRD.md section
// 11a, docs/PHASE5_IMPLEMENTATION.md Unit J): a RECORD_TYPE_MANDATE retry
// can never be scheduled sooner than mandateLeadTime from now (the RBI
// e-mandate framework's pre-debit notification requirement), applied as a
// floor over whatever the bucket-specific timing above already computed,
// after it, never before it: the salary-window calculation may still push
// the date later, only the floor may pull an earlier date up to itself.
//
// Pure function: no I/O, no struct methods. Takes now directly rather
// than an injected Clock, matching dueAtFor's existing signature style.
func retryDueAt(bucket commonv1.RootCauseBucket, recordType commonv1.RecordType, now time.Time, retryDelay time.Duration, mandateLeadTime time.Duration, timeScale float64) *time.Time {
	due := baseRetryDueAt(bucket, now, retryDelay, timeScale)
	if due == nil {
		return nil
	}
	if recordType == commonv1.RecordType_RECORD_TYPE_MANDATE {
		floored := mandateLeadTimeFloor(*due, now, mandateLeadTime, timeScale)
		due = &floored
	}
	return due
}

// baseRetryDueAt is retryDueAt's bucket-timing switch, factored out so the
// MANDATE lead-time floor above applies uniformly to every branch's result
// in one place, rather than being threaded into each case.
func baseRetryDueAt(bucket commonv1.RootCauseBucket, now time.Time, retryDelay time.Duration, timeScale float64) *time.Time {
	switch bucket {
	case commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_INSUFFICIENT_FUNDS:
		return salaryWindowDueAt(now, timeScale)

	case commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK:
		// Funds were there, the rail was busy. Short backoff, reuse
		// the existing config value.
		due := now.Add(scaleDuration(retryDelay, timeScale))
		return &due

	case commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_HARD_DECLINE,
		commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_RISK_HOLD:
		// HARD_DECLINE: a retry cannot succeed, only a method update
		// can. RISK_HOLD: never auto-retry around a risk decision; in
		// practice this bucket escalates at classify time and never
		// reaches a retry-scheduling call, but we encode it here as a
		// second independent stop.
		return nil

	default:
		// Everything else (UNSPECIFIED, USER_ACTION_NEEDED,
		// ABANDONMENT, OVERDUE, or any future bucket): fall back to
		// the flat RetryDelay, do not guess.
		due := now.Add(scaleDuration(retryDelay, timeScale))
		return &due
	}
}

// mandateLeadTimeFloor enforces the RBI Digital Payments E-mandate
// Framework's pre-transaction notification requirement: an auto-debit
// retry on a MANDATE record can never be scheduled sooner than leadTime
// from now. A floor, not an offset: due passes through unchanged whenever
// it already lands at or after the floor (e.g. a salary-window date weeks
// out), and is only ever pulled later, never earlier. The floor's own wait
// is scaled by timeScale like every other timing knob, so it cannot stall
// a compressed demo run for a real 24 hours.
func mandateLeadTimeFloor(due time.Time, now time.Time, leadTime time.Duration, timeScale float64) time.Time {
	floor := now.Add(scaleDuration(leadTime, timeScale))
	if due.Before(floor) {
		return floor
	}
	return due
}

// istZone is India Standard Time, a fixed UTC+5:30 offset with no daylight
// saving, so time.FixedZone is correct and simpler than a tzdata lookup (a
// distroless runtime image may not carry tzdata at all).
var istZone = time.FixedZone("IST", 5*3600+30*60)

// contactWindowOpenHour and contactWindowCloseHour are TRAI TCCCPR 2018's
// commercial-communication contact-hour window in IST, half-open
// [10, 21): a due time landing at hour 21 or later, or before hour 10, is
// outside the window (docs/PRD.md section 11a).
const (
	contactWindowOpenHour  = 10
	contactWindowCloseHour = 21
)

// contactHourWindow enforces the TRAI contact-hour rule for a
// customer-contacting action's due time: unchanged if it already falls
// inside 10:00 to 21:00 IST, deferred to the next window open otherwise,
// never dropped. Callers only ever pass a due time computed for a nudge
// (dueAtFor returns nil for every state that is not NUDGE_SCHEDULED before
// this is reached), so there is no need to also check the action type
// here. The extra wait until the window opens, not the whole due time, is
// scaled by timeScale, matching every other timing knob, so this rule
// cannot stall a compressed demo run for real wall-clock hours.
func contactHourWindow(due time.Time, timeScale float64) time.Time {
	ist := due.In(istZone)
	hour := ist.Hour()
	if hour >= contactWindowOpenHour && hour < contactWindowCloseHour {
		return due
	}

	nextOpen := time.Date(ist.Year(), ist.Month(), ist.Day(), contactWindowOpenHour, 0, 0, 0, istZone)
	if hour >= contactWindowCloseHour {
		nextOpen = nextOpen.AddDate(0, 0, 1)
	}

	wait := nextOpen.Sub(ist)
	return due.Add(scaleDuration(wait, timeScale))
}

// salaryWindowDueAt returns when the next salary-credit window opens.
// The window is days 1 through 7 of each calendar month (inclusive on
// both ends). If the current day-of-month is inside the window [1, 7],
// the window is open now and we return now (no delay: the money should
// arrive imminently, and the scheduler will pick it up on the next tick).
// If day 8 or later, the next window is the 1st of the next calendar
// month at the same wall-clock time-of-day as now.
func salaryWindowDueAt(now time.Time, timeScale float64) *time.Time {
	day := now.Day()
	if day >= 1 && day <= 7 {
		// Window is open now: return now (with timeScale applied, a
		// scale of 1 keeps it at now).
		due := now.Add(scaleDuration(0, timeScale))
		return &due
	}

	// Next window: 1st of next calendar month, same time-of-day.
	// Go's time.Date normalises month 13 to January of the next year
	// automatically, but we pass month+1 explicitly and let it handle
	// the rollover.
	nextFirst := time.Date(now.Year(), now.Month()+1, 1,
		now.Hour(), now.Minute(), now.Second(), now.Nanosecond(), now.Location())

	wait := nextFirst.Sub(now)
	due := now.Add(scaleDuration(wait, timeScale))
	return &due
}

// scaleDuration divides d by timeScale, matching config.Common.Scale's
// formula (internal/platform/config/config.go). A timeScale of 1 is a
// no-op. Values greater than 1 compress; values between 0 and 1 stretch.
func scaleDuration(d time.Duration, timeScale float64) time.Duration {
	if timeScale == 1 {
		return d
	}
	return time.Duration(float64(d) / timeScale)
}
