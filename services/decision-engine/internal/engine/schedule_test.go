package engine

import (
	"testing"
	"time"

	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

func TestRetryDueAt(t *testing.T) {
	const retryDelay = 30 * time.Minute
	const mandateLeadTime = 24 * time.Hour

	tests := []struct {
		name      string
		bucket    commonv1.RootCauseBucket
		now       time.Time
		wantNil   bool
		wantExact time.Time // when non-nil, the exact expected due_at
	}{
		{
			name:      "transient bank uses flat retryDelay",
			bucket:    commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK,
			now:       time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC),
			wantNil:   false,
			wantExact: time.Date(2026, 8, 24, 12, 30, 0, 0, time.UTC),
		},
		{
			name:    "hard decline never retries",
			bucket:  commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_HARD_DECLINE,
			now:     time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC),
			wantNil: true,
		},
		{
			name:    "risk hold never retries",
			bucket:  commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_RISK_HOLD,
			now:     time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC),
			wantNil: true,
		},
		{
			name:      "unspecified bucket falls back to flat retryDelay",
			bucket:    commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_UNSPECIFIED,
			now:       time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC),
			wantNil:   false,
			wantExact: time.Date(2026, 8, 24, 12, 30, 0, 0, time.UTC),
		},
		{
			name:      "abandonment bucket falls back to flat retryDelay",
			bucket:    commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_ABANDONMENT,
			now:       time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC),
			wantNil:   false,
			wantExact: time.Date(2026, 8, 24, 12, 30, 0, 0, time.UTC),
		},
		{
			name:      "overdue bucket falls back to flat retryDelay",
			bucket:    commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_OVERDUE,
			now:       time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC),
			wantNil:   false,
			wantExact: time.Date(2026, 8, 24, 12, 30, 0, 0, time.UTC),
		},
		{
			name:      "insufficient funds on the 3rd: same-month salary window, returns now",
			bucket:    commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_INSUFFICIENT_FUNDS,
			now:       time.Date(2026, 8, 3, 14, 22, 0, 0, time.UTC),
			wantNil:   false,
			wantExact: time.Date(2026, 8, 3, 14, 22, 0, 0, time.UTC),
		},
		{
			name:      "insufficient funds on the 7th: last day of salary window, returns now",
			bucket:    commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_INSUFFICIENT_FUNDS,
			now:       time.Date(2026, 8, 7, 23, 59, 0, 0, time.UTC),
			wantNil:   false,
			wantExact: time.Date(2026, 8, 7, 23, 59, 0, 0, time.UTC),
		},
		{
			name:      "insufficient funds well past the window: next month 1st, same time-of-day",
			bucket:    commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_INSUFFICIENT_FUNDS,
			now:       time.Date(2026, 8, 28, 15, 30, 0, 0, time.UTC),
			wantNil:   false,
			wantExact: time.Date(2026, 9, 1, 15, 30, 0, 0, time.UTC),
		},
		{
			// The precise boundary the "on the 7th" case above doesn't
			// pin down on its own: day 8 is the first day OUTSIDE the
			// window and must fall through to next-month behavior, not
			// be treated as still-open. Catches an off-by-one on the
			// upper bound specifically (e.g. day <= 8 instead of <= 7).
			name:      "insufficient funds on the 8th: first day outside the window, next month 1st",
			bucket:    commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_INSUFFICIENT_FUNDS,
			now:       time.Date(2026, 8, 8, 9, 0, 0, 0, time.UTC),
			wantNil:   false,
			wantExact: time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC),
		},
		{
			name:      "insufficient funds month boundary: 31-day month to next month 1st",
			bucket:    commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_INSUFFICIENT_FUNDS,
			now:       time.Date(2026, 7, 31, 9, 0, 0, 0, time.UTC),
			wantNil:   false,
			wantExact: time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC),
		},
		{
			name:      "insufficient funds year rollover: December 28 to January 1",
			bucket:    commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_INSUFFICIENT_FUNDS,
			now:       time.Date(2026, 12, 28, 10, 45, 0, 0, time.UTC),
			wantNil:   false,
			wantExact: time.Date(2027, 1, 1, 10, 45, 0, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := retryDueAt(tt.bucket, commonv1.RecordType_RECORD_TYPE_PAYMENT, tt.now, retryDelay, mandateLeadTime, 1)
			if tt.wantNil {
				if got != nil {
					t.Errorf("retryDueAt(%v, %v) = %v, want nil", tt.bucket, tt.now, *got)
				}
				return
			}
			if got == nil {
				t.Fatalf("retryDueAt(%v, %v) = nil, want %v", tt.bucket, tt.now, tt.wantExact)
			}
			if !got.Equal(tt.wantExact) {
				t.Errorf("retryDueAt(%v, %v) = %v, want %v", tt.bucket, tt.now, *got, tt.wantExact)
			}
		})
	}
}

func TestRetryDueAtTimeScaleCompression(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	const retryDelay = 30 * time.Minute
	const mandateLeadTime = 24 * time.Hour
	const timeScale float64 = 3600

	got := retryDueAt(commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_INSUFFICIENT_FUNDS, commonv1.RecordType_RECORD_TYPE_PAYMENT, now, retryDelay, mandateLeadTime, timeScale)
	if got == nil {
		t.Fatal("retryDueAt returned nil for INSUFFICIENT_FUNDS")
	}

	// Without timeScale, due_at would be 2026-09-01 12:00:00 UTC (4 days away).
	// With timeScale=3600 (1 hour of wall time per second of demo time),
	// the 4-day wait compresses to 4*24/3600 = 0.2667 hours ≈ 16 minutes.
	// The exact formula: scaledDelay = duration / timeScale,
	// so due_at = now + (nextWindow - now)/timeScale.
	wait := got.Sub(now)
	wantWait := time.Duration(float64(4*24*time.Hour) / timeScale)
	if wait != wantWait {
		t.Errorf("wait = %v, want %v (timeScale=%v compresses 4-day wait)", wait, wantWait, timeScale)
	}
}

func TestRetryDueAtTimeScaleOneIsNoop(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	const retryDelay = 30 * time.Minute
	const mandateLeadTime = 24 * time.Hour

	got := retryDueAt(commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_INSUFFICIENT_FUNDS, commonv1.RecordType_RECORD_TYPE_PAYMENT, now, retryDelay, mandateLeadTime, 1)
	if got == nil {
		t.Fatal("retryDueAt returned nil for INSUFFICIENT_FUNDS")
	}

	want := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("timeScale=1: got %v, want %v", *got, want)
	}
}

// A MANDATE retry cannot be scheduled sooner than the RBI e-mandate lead
// time from now (docs/PRD.md section 11a), even though TRANSIENT_BANK's
// own timing would otherwise put it minutes away.
func TestRetryDueAtFloorsMandateRetriesToTheLeadTime(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	const retryDelay = 30 * time.Minute
	const mandateLeadTime = 24 * time.Hour

	got := retryDueAt(commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK, commonv1.RecordType_RECORD_TYPE_MANDATE, now, retryDelay, mandateLeadTime, 1)
	if got == nil {
		t.Fatal("retryDueAt returned nil for TRANSIENT_BANK")
	}
	want := now.Add(mandateLeadTime)
	if !got.Equal(want) {
		t.Errorf("MANDATE retry due_at = %v, want the 24h floor %v, not TRANSIENT_BANK's own 30 minute delay", *got, want)
	}
}

// A PAYMENT (non-MANDATE) retry must be completely unaffected by the
// mandate lead time: this is the guard against the floor leaking onto
// record types it was never meant for.
func TestRetryDueAtDoesNotFloorNonMandateRecords(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	const retryDelay = 30 * time.Minute
	const mandateLeadTime = 24 * time.Hour

	got := retryDueAt(commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK, commonv1.RecordType_RECORD_TYPE_PAYMENT, now, retryDelay, mandateLeadTime, 1)
	if got == nil {
		t.Fatal("retryDueAt returned nil for TRANSIENT_BANK")
	}
	want := now.Add(retryDelay)
	if !got.Equal(want) {
		t.Errorf("PAYMENT retry due_at = %v, want the plain retryDelay %v, the mandate floor must not apply", *got, want)
	}
}

// A salary-window date that already lands weeks out must NOT be pulled
// earlier by the mandate floor: it is a floor, never an offset.
func TestRetryDueAtMandateFloorNeverPullsALaterDateEarlier(t *testing.T) {
	now := time.Date(2026, 8, 8, 9, 0, 0, 0, time.UTC) // past this month's window
	const retryDelay = 30 * time.Minute
	const mandateLeadTime = 24 * time.Hour

	got := retryDueAt(commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_INSUFFICIENT_FUNDS, commonv1.RecordType_RECORD_TYPE_MANDATE, now, retryDelay, mandateLeadTime, 1)
	if got == nil {
		t.Fatal("retryDueAt returned nil for INSUFFICIENT_FUNDS")
	}
	want := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC) // next month's 1st, unaffected by the 24h floor
	if !got.Equal(want) {
		t.Errorf("due_at = %v, want the salary window's own %v; a floor must not pull a later date earlier", *got, want)
	}
}

// TestContactHourWindow pins the TRAI TCCCPR contact-hour rule (docs/PRD.md
// section 11a) against fixed IST instants, not time.Now().
func TestContactHourWindow(t *testing.T) {
	tests := []struct {
		name string
		due  time.Time // UTC, converted to IST inside contactHourWindow
		want time.Time
	}{
		{
			name: "inside the window, unchanged",
			due:  time.Date(2026, 8, 24, 8, 30, 0, 0, time.UTC), // 14:00 IST
			want: time.Date(2026, 8, 24, 8, 30, 0, 0, time.UTC),
		},
		{
			name: "exactly 10:00 IST, the window's own open edge, unchanged",
			due:  time.Date(2026, 8, 24, 4, 30, 0, 0, time.UTC), // 10:00 IST
			want: time.Date(2026, 8, 24, 4, 30, 0, 0, time.UTC),
		},
		{
			name: "exactly 20:59 IST, one minute before close, unchanged",
			due:  time.Date(2026, 8, 24, 15, 29, 0, 0, time.UTC), // 20:59 IST
			want: time.Date(2026, 8, 24, 15, 29, 0, 0, time.UTC),
		},
		{
			name: "exactly 21:00 IST, the close edge, deferred to next day 10:00",
			due:  time.Date(2026, 8, 24, 15, 30, 0, 0, time.UTC), // 21:00 IST
			want: time.Date(2026, 8, 25, 4, 30, 0, 0, time.UTC),  // next day 10:00 IST
		},
		{
			// due's UTC date is the 24th but its IST date has already
			// rolled to the 25th (UTC+5:30 crosses midnight); the "next
			// 10:00" must be computed against the IST date, not the UTC
			// one, or this lands a day early.
			name: "past midnight IST, deferred to that IST day's 10:00",
			due:  time.Date(2026, 8, 24, 20, 0, 0, 0, time.UTC), // 01:30 IST, 25th
			want: time.Date(2026, 8, 25, 4, 30, 0, 0, time.UTC), // 10:00 IST, 25th
		},
		{
			name: "before 10:00 IST, deferred to the same day's 10:00",
			due:  time.Date(2026, 8, 24, 2, 0, 0, 0, time.UTC),  // 07:30 IST
			want: time.Date(2026, 8, 24, 4, 30, 0, 0, time.UTC), // 10:00 IST, same day
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := contactHourWindow(tt.due, 1)
			if !got.Equal(tt.want) {
				t.Errorf("contactHourWindow(%v) = %v, want %v", tt.due, got, tt.want)
			}
		})
	}
}

// The extra wait until the window opens, not the whole due time, must be
// scaled by timeScale, matching every other timing knob, or this rule
// would stall a compressed demo run for real wall-clock hours.
func TestContactHourWindowScalesTheExtraWaitOnly(t *testing.T) {
	due := time.Date(2026, 8, 24, 15, 30, 0, 0, time.UTC) // exactly 21:00 IST, deferred
	const timeScale float64 = 3600

	got := contactHourWindow(due, timeScale)
	wait := got.Sub(due)
	wantWait := time.Duration(float64(13*time.Hour) / timeScale) // 21:00 -> next day 10:00 is 13h
	if wait != wantWait {
		t.Errorf("wait = %v, want %v (timeScale=%v compresses the 13h wait)", wait, wantWait, timeScale)
	}
}

func TestMandateLeadTimeFloor(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	const leadTime = 24 * time.Hour

	// Due sooner than the floor: pulled up to exactly the floor.
	soon := now.Add(5 * time.Minute)
	if got := mandateLeadTimeFloor(soon, now, leadTime, 1); !got.Equal(now.Add(leadTime)) {
		t.Errorf("floor(due=%v) = %v, want %v", soon, got, now.Add(leadTime))
	}

	// Due already later than the floor: passes through unchanged.
	later := now.Add(30 * 24 * time.Hour)
	if got := mandateLeadTimeFloor(later, now, leadTime, 1); !got.Equal(later) {
		t.Errorf("floor(due=%v) = %v, want unchanged %v", later, got, later)
	}

	// Due exactly at the floor: passes through unchanged (not strictly
	// after, but Before is false so it is not re-floored onto itself).
	exact := now.Add(leadTime)
	if got := mandateLeadTimeFloor(exact, now, leadTime, 1); !got.Equal(exact) {
		t.Errorf("floor(due=%v) = %v, want unchanged %v", exact, got, exact)
	}
}
