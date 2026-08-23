//go:build integration

package server

import (
	"context"
	"testing"

	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

func TestScanRecordsWithEmptyBatchIDChecksEverything(t *testing.T) {
	// Regression: WHERE $1 = '' OR r.batch_id = $1::uuid fails with
	// "invalid input syntax for type uuid" for an empty $1, because
	// Postgres does not short-circuit OR the way a procedural language
	// would; both branches are evaluated. scanRecords must not error when
	// no batch filter is requested. See docs/INCIDENTS.md.
	pool := testPool(t)
	ctx := context.Background()
	_, recordID := seedRecord(ctx, t, pool)

	st := newStore(pool)
	snapshots, err := st.scanRecords(ctx, "")
	if err != nil {
		t.Fatalf("scanRecords(\"\"): %v", err)
	}

	found := false
	for _, s := range snapshots {
		if s.RecordID == recordID {
			found = true
		}
	}
	if !found {
		t.Errorf("scanRecords(\"\") did not include seeded record %s", recordID)
	}
}

func TestScanRecordsFiltersByBatchID(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	batchA, recordA := seedRecord(ctx, t, pool)
	_, recordB := seedRecord(ctx, t, pool)

	st := newStore(pool)
	snapshots, err := st.scanRecords(ctx, batchA)
	if err != nil {
		t.Fatalf("scanRecords(%q): %v", batchA, err)
	}

	ids := make(map[string]bool)
	for _, s := range snapshots {
		ids[s.RecordID] = true
	}
	if !ids[recordA] {
		t.Errorf("batch filter excluded its own record %s", recordA)
	}
	if ids[recordB] {
		t.Errorf("batch filter included a record from a different batch: %s", recordB)
	}
}

// scanRecords compares batch_id as text rather than casting the parameter
// to uuid (see the empty-string regression above), so a malformed,
// non-empty filter does not error here, it simply matches nothing.
// Server.VerifyInvariants rejects a malformed batch_id before it ever
// reaches scanRecords (server.go); this documents that scanRecords itself
// is defense-in-depth, not the only guard.
func TestScanRecordsWithMalformedBatchIDMatchesNothingRatherThanErroring(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	_, recordID := seedRecord(ctx, t, pool)

	st := newStore(pool)
	snapshots, err := st.scanRecords(ctx, "not-a-uuid")
	if err != nil {
		t.Fatalf("scanRecords(%q): %v", "not-a-uuid", err)
	}
	for _, s := range snapshots {
		if s.RecordID == recordID {
			t.Errorf("a malformed batch filter matched a real record: %s", recordID)
		}
	}
}

func TestScanRecordsReportsNoStateForUnprocessedRecords(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	_, recordID := seedRecord(ctx, t, pool)

	st := newStore(pool)
	snapshots, err := st.scanRecords(ctx, "")
	if err != nil {
		t.Fatalf("scanRecords: %v", err)
	}

	for _, s := range snapshots {
		if s.RecordID != recordID {
			continue
		}
		if s.HasState {
			t.Errorf("HasState = true for a record never touched by Decision Engine")
		}
		if len(s.Entries) != 0 {
			t.Errorf("Entries = %v, want empty for an unprocessed record", s.Entries)
		}
		return
	}
	t.Fatalf("seeded record %s not found in scan", recordID)
}

func TestScanRecordsOrdersEntriesOldestFirst(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	batchID, recordID := seedRecord(ctx, t, pool)
	seedRecordState(ctx, t, pool, recordID, commonv1.RecordState_RECORD_STATE_RECOVERED.String())

	seedAuditEntry(ctx, t, pool, recordID, batchID,
		commonv1.RecordState_RECORD_STATE_NEW.String(), commonv1.RecordState_RECORD_STATE_SCORING.String())
	seedAuditEntry(ctx, t, pool, recordID, batchID,
		commonv1.RecordState_RECORD_STATE_SCORING.String(), commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED.String())

	st := newStore(pool)
	snapshots, err := st.scanRecords(ctx, "")
	if err != nil {
		t.Fatalf("scanRecords: %v", err)
	}

	for _, s := range snapshots {
		if s.RecordID != recordID {
			continue
		}
		if len(s.Entries) != 2 {
			t.Fatalf("len(Entries) = %d, want 2", len(s.Entries))
		}
		if s.Entries[0].To != commonv1.RecordState_RECORD_STATE_SCORING {
			t.Errorf("Entries[0].To = %v, want RECORD_STATE_SCORING (oldest first)", s.Entries[0].To)
		}
		if s.Entries[1].To != commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED {
			t.Errorf("Entries[1].To = %v, want RECORD_STATE_RETRY_SCHEDULED", s.Entries[1].To)
		}
		return
	}
	t.Fatalf("seeded record %s not found in scan", recordID)
}
