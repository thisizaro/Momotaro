//go:build integration

// loadDowntimeStatus and recordDowntimeEvent are the store boundary for
// docs/PHASE5_5_IMPLEMENTATION.md Unit Y's PAYMENT_DOWNTIME table. These
// tests run against real Postgres because the upsert-and-resolve semantics
// ARE the behaviour (docs/ENGINEERING.md section 1, do not mock what you
// own): guardrails_test.go and downtime_test.go already cover the pure
// decision logic (downtimeBlocksRetry, applyDowntimeGuardrail) without a
// database at all.
package engine

import (
	"context"
	"testing"
	"time"

	pgxpkg "github.com/thisizaro/Momotaro/internal/platform/pgx"
)

func cleanupDowntime(t *testing.T, pool *pgxpkg.Pool, id string) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM payment_downtime WHERE id = $1`, id)
	})
}

func TestLoadDowntimeStatusFindsNothingForAnInstrumentWithNoRows(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)

	status, err := newStore(pool).loadDowntimeStatus(ctx, "VIJB-"+t.Name())
	if err != nil {
		t.Fatalf("loadDowntimeStatus: %v", err)
	}
	if status.Present {
		t.Error("Present = true, want false: no row exists for this instrument")
	}
}

func TestLoadDowntimeStatusReturnsEmptyForABlankInstrumentRef(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)

	status, err := newStore(pool).loadDowntimeStatus(ctx, "")
	if err != nil {
		t.Fatalf("loadDowntimeStatus: %v", err)
	}
	if status.Present {
		t.Error("Present = true, want false for an empty instrument_ref")
	}
}

// recordDowntimeEvent for a "started" status must upsert a row
// loadDowntimeStatus then finds Present, with every field carried through.
func TestRecordDowntimeEventThenLoadRoundTripsAStartedEvent(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	s := newStore(pool)

	instrument := "VIJB-" + t.Name()
	begin := time.Date(2026, 6, 12, 5, 0, 38, 0, time.UTC)
	now := begin

	if err := s.recordDowntimeEvent(ctx, DowntimeEvent{
		DowntimeID: "down_test_" + t.Name(),
		Method:     "netbanking",
		Status:     "started",
		Scheduled:  false,
		Severity:   "high",
		Instrument: instrument,
		Begin:      begin,
		End:        nil,
		Now:        now,
	}); err != nil {
		t.Fatalf("recordDowntimeEvent: %v", err)
	}
	cleanupDowntime(t, pool, "down_test_"+t.Name())

	status, err := s.loadDowntimeStatus(ctx, instrument)
	if err != nil {
		t.Fatalf("loadDowntimeStatus: %v", err)
	}
	if !status.Present {
		t.Fatal("Present = false, want true after a started event")
	}
	if status.Method != "netbanking" || status.Severity != "high" || status.Scheduled {
		t.Errorf("status = %+v, want method=netbanking severity=high scheduled=false", status)
	}
	if status.End != nil {
		t.Errorf("End = %v, want nil: this event had end=nil", *status.End)
	}
	if !status.Begin.Equal(begin) {
		t.Errorf("Begin = %v, want %v", status.Begin, begin)
	}
}

// A "resolved" event for the same downtime_id must make loadDowntimeStatus
// stop finding it: this is the entire resume mechanism
// (docs/PHASE5_5_IMPLEMENTATION.md Unit Y), so it is worth pinning directly.
func TestRecordDowntimeEventResolvedMakesItStopBeingPresent(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	s := newStore(pool)

	id := "down_test_" + t.Name()
	instrument := "VIJB-" + t.Name()
	begin := time.Date(2026, 6, 12, 5, 0, 38, 0, time.UTC)

	if err := s.recordDowntimeEvent(ctx, DowntimeEvent{
		DowntimeID: id, Method: "netbanking", Status: "started", Severity: "high",
		Instrument: instrument, Begin: begin, Now: begin,
	}); err != nil {
		t.Fatalf("recordDowntimeEvent (started): %v", err)
	}
	cleanupDowntime(t, pool, id)

	resolvedAt := begin.Add(time.Hour)
	if err := s.recordDowntimeEvent(ctx, DowntimeEvent{
		DowntimeID: id, Method: "netbanking", Status: "resolved", Severity: "high",
		Instrument: instrument, Begin: begin, End: &resolvedAt, Now: resolvedAt,
	}); err != nil {
		t.Fatalf("recordDowntimeEvent (resolved): %v", err)
	}

	status, err := s.loadDowntimeStatus(ctx, instrument)
	if err != nil {
		t.Fatalf("loadDowntimeStatus: %v", err)
	}
	if status.Present {
		t.Error("Present = true, want false: the downtime was resolved")
	}
}

// Two events with the same downtime_id (started, then updated) must
// collapse to one row, never accumulate a history nothing reads.
func TestRecordDowntimeEventUpsertsRatherThanAccumulating(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	s := newStore(pool)

	id := "down_test_" + t.Name()
	instrument := "VIJB-" + t.Name()
	begin := time.Date(2026, 6, 12, 5, 0, 38, 0, time.UTC)

	if err := s.recordDowntimeEvent(ctx, DowntimeEvent{
		DowntimeID: id, Method: "netbanking", Status: "started", Severity: "high",
		Instrument: instrument, Begin: begin, Now: begin,
	}); err != nil {
		t.Fatalf("recordDowntimeEvent (started): %v", err)
	}
	cleanupDowntime(t, pool, id)

	if err := s.recordDowntimeEvent(ctx, DowntimeEvent{
		DowntimeID: id, Method: "netbanking", Status: "updated", Severity: "medium",
		Instrument: instrument, Begin: begin, Now: begin.Add(10 * time.Minute),
	}); err != nil {
		t.Fatalf("recordDowntimeEvent (updated): %v", err)
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM payment_downtime WHERE id = $1`, id).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("row count for downtime_id %s = %d, want 1 (upsert, not accumulate)", id, count)
	}

	status, err := s.loadDowntimeStatus(ctx, instrument)
	if err != nil {
		t.Fatalf("loadDowntimeStatus: %v", err)
	}
	if status.Severity != "medium" {
		t.Errorf("Severity = %q, want %q: the updated event's value should win", status.Severity, "medium")
	}
}
