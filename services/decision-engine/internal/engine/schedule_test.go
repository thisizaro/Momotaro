package engine

import (
	"testing"
	"time"

	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

func TestRetryDueAt(t *testing.T) {
	const retryDelay = 30 * time.Minute

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
			name:      "insufficient funds on the 8th: next month 1st, same time-of-day",
			bucket:    commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_INSUFFICIENT_FUNDS,
			now:       time.Date(2026, 8, 28, 15, 30, 0, 0, time.UTC),
			wantNil:   false,
			wantExact: time.Date(2026, 9, 1, 15, 30, 0, 0, time.UTC),
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
			got := retryDueAt(tt.bucket, tt.now, retryDelay, 1)
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
	const timeScale float64 = 3600

	got := retryDueAt(commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_INSUFFICIENT_FUNDS, now, retryDelay, timeScale)
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

	got := retryDueAt(commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_INSUFFICIENT_FUNDS, now, retryDelay, 1)
	if got == nil {
		t.Fatal("retryDueAt returned nil for INSUFFICIENT_FUNDS")
	}

	want := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("timeScale=1: got %v, want %v", *got, want)
	}
}
