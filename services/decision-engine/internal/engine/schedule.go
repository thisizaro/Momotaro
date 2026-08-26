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
// Pure function: no I/O, no struct methods. Takes now directly rather
// than an injected Clock, matching dueAtFor's existing signature style.
func retryDueAt(bucket commonv1.RootCauseBucket, now time.Time, retryDelay time.Duration, timeScale float64) *time.Time {
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
