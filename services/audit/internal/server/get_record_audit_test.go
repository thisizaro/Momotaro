//go:build integration

// GetRecordAudit exercises real Postgres rather than a mock, per
// docs/ENGINEERING.md section 1 ("do not mock what you own"). They
// therefore need the docker-compose stack up, so they sit behind the
// `integration` build tag. Run with `make test-integration`.

package server

import (
	"context"
	"testing"

	"github.com/google/uuid"
	auditv1 "github.com/thisizaro/Momotaro/proto/gen/audit/v1"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestGetRecordAuditReturnsRecordStateAndEntries(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	batchID, recordID := seedRecord(ctx, t, pool)

	seedRecordState(ctx, t, pool, recordID, "RECORD_STATE_RECOVERED")
	// The four transitions the live pipeline actually writes: classify into
	// Scoring, the scorer's decision, the scheduler claiming the due record,
	// then the execute outcome. scheduleNew (decision-engine store.go) writes
	// the classification rationale and source on every step it inserts, not
	// only the last, so both the New->Scoring and Scoring->RetryScheduled
	// rows carry it here too.
	//
	// This fixture used to open with a fabricated 'ingested'
	// UNSPECIFIED -> NEW entry. Nothing in the system writes that (Ingestion
	// writes no audit rows at all, docs/ARCHITECTURE.md section 10a) and the
	// state machine forbids UNSPECIFIED as a from-state, so the trail was
	// invalid. It went unnoticed because trail_complete was hardcoded true.
	// See docs/INCIDENTS.md 2026-08-23.
	if _, err := pool.Exec(ctx, `
		INSERT INTO audit_entry (record_id, batch_id, ts, from_state, to_state, reason, rationale, source, actor, attempt_number, cost_paise)
		VALUES
		 ($1, $2, now() - interval '3 minutes', 'RECORD_STATE_NEW', 'RECORD_STATE_SCORING', 'classified, guardrails applied, scoring', 'transient bank failure, retry', 'SOURCE_RULES_FALLBACK', 'system', 0, NULL),
		 ($1, $2, now() - interval '2 minutes', 'RECORD_STATE_SCORING', 'RECORD_STATE_RETRY_SCHEDULED', 'classified, retry scheduled', 'transient bank failure, retry', 'SOURCE_RULES_FALLBACK', 'system', 0, NULL),
		 ($1, $2, now() - interval '1 minute', 'RECORD_STATE_RETRY_SCHEDULED', 'RECORD_STATE_RETRYING', 'scheduler claimed due record', NULL, NULL, 'system', 0, NULL),
		 ($1, $2, now(), 'RECORD_STATE_RETRYING', 'RECORD_STATE_RECOVERED', 'action succeeded', NULL, NULL, 'system', 1, 0)
	`, recordID, batchID); err != nil {
		t.Fatalf("seed audit_entry: %v", err)
	}

	s := New(pool)
	resp, err := s.GetRecordAudit(ctx, &auditv1.GetRecordAuditRequest{RecordId: recordID})
	if err != nil {
		t.Fatalf("GetRecordAudit: %v", err)
	}

	if resp.Record.Id != recordID {
		t.Errorf("Record.Id = %q, want %q", resp.Record.Id, recordID)
	}
	if resp.Record.AmountPaise != 12345 {
		t.Errorf("Record.AmountPaise = %d, want 12345", resp.Record.AmountPaise)
	}
	if resp.Record.Type != commonv1.RecordType_RECORD_TYPE_PAYMENT {
		t.Errorf("Record.Type = %v, want RECORD_TYPE_PAYMENT", resp.Record.Type)
	}
	if resp.CurrentState != commonv1.RecordState_RECORD_STATE_RECOVERED {
		t.Errorf("CurrentState = %v, want RECORD_STATE_RECOVERED", resp.CurrentState)
	}
	if !resp.TrailComplete {
		t.Error("TrailComplete = false, want true")
	}
	if len(resp.Entries) != 4 {
		t.Fatalf("len(Entries) = %d, want 4 (classify, score, claim, outcome)", len(resp.Entries))
	}

	// Oldest first.
	if resp.Entries[0].ToState != commonv1.RecordState_RECORD_STATE_SCORING {
		t.Errorf("Entries[0].ToState = %v, want RECORD_STATE_SCORING", resp.Entries[0].ToState)
	}
	if resp.Entries[0].Rationale != "transient bank failure, retry" {
		t.Errorf("Entries[0].Rationale = %q, want the classifier rationale", resp.Entries[0].Rationale)
	}
	if resp.Entries[0].Source != commonv1.Source_SOURCE_RULES_FALLBACK {
		t.Errorf("Entries[0].Source = %v, want SOURCE_RULES_FALLBACK", resp.Entries[0].Source)
	}
	if resp.Entries[1].ToState != commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED {
		t.Errorf("Entries[1].ToState = %v, want RECORD_STATE_RETRY_SCHEDULED", resp.Entries[1].ToState)
	}
	if resp.Entries[3].ToState != commonv1.RecordState_RECORD_STATE_RECOVERED {
		t.Errorf("Entries[3].ToState = %v, want RECORD_STATE_RECOVERED", resp.Entries[3].ToState)
	}
	if resp.Entries[3].AttemptNumber != 1 {
		t.Errorf("Entries[3].AttemptNumber = %d, want 1", resp.Entries[3].AttemptNumber)
	}
}

func TestGetRecordAuditRecordNotFound(t *testing.T) {
	pool := testPool(t)
	s := New(pool)

	_, err := s.GetRecordAudit(context.Background(), &auditv1.GetRecordAuditRequest{RecordId: uuid.NewString()})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("GetRecordAudit for unknown record: err = %v, want NotFound", err)
	}
}

func TestGetRecordAuditMissingRecordID(t *testing.T) {
	pool := testPool(t)
	s := New(pool)

	_, err := s.GetRecordAudit(context.Background(), &auditv1.GetRecordAuditRequest{})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("GetRecordAudit with no record_id: err = %v, want InvalidArgument", err)
	}
}

// A record that has been ingested but not yet touched by Decision Engine
// has no record_state row yet. That must not be an error.
func TestGetRecordAuditWithNoStateYet(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	_, recordID := seedRecord(ctx, t, pool)

	s := New(pool)
	resp, err := s.GetRecordAudit(ctx, &auditv1.GetRecordAuditRequest{RecordId: recordID})
	if err != nil {
		t.Fatalf("GetRecordAudit: %v", err)
	}
	if resp.CurrentState != commonv1.RecordState_RECORD_STATE_UNSPECIFIED {
		t.Errorf("CurrentState = %v, want RECORD_STATE_UNSPECIFIED", resp.CurrentState)
	}
	if len(resp.Entries) != 0 {
		t.Errorf("len(Entries) = %d, want 0", len(resp.Entries))
	}
}

// trail_complete used to be hardcoded true, which made every assertion on it
// (including the end-to-end test's) vacuous. These two prove it is now
// actually derived from the trail, in both directions, through the real RPC.
// See docs/INCIDENTS.md 2026-08-23.
func TestGetRecordAuditReportsTrailCompleteForASoundTrail(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	batchID, recordID := seedRecord(ctx, t, pool)
	seedRecordState(ctx, t, pool, recordID, "RECORD_STATE_RECOVERED")
	// The real shape the live pipeline writes: classify into Scoring, the
	// scorer's decision, the scheduler claiming the due record, outcome.
	seedAuditEntry(ctx, t, pool, recordID, batchID, "RECORD_STATE_NEW", "RECORD_STATE_SCORING")
	seedAuditEntry(ctx, t, pool, recordID, batchID, "RECORD_STATE_SCORING", "RECORD_STATE_RETRY_SCHEDULED")
	seedAuditEntry(ctx, t, pool, recordID, batchID, "RECORD_STATE_RETRY_SCHEDULED", "RECORD_STATE_RETRYING")
	seedAuditEntry(ctx, t, pool, recordID, batchID, "RECORD_STATE_RETRYING", "RECORD_STATE_RECOVERED")

	s := New(pool)
	resp, err := s.GetRecordAudit(ctx, &auditv1.GetRecordAuditRequest{RecordId: recordID})
	if err != nil {
		t.Fatalf("GetRecordAudit: %v", err)
	}
	if !resp.TrailComplete {
		t.Errorf("TrailComplete = false for a sound trail: %v", resp.Entries)
	}
}

func TestGetRecordAuditReportsTrailIncompleteWhenAnEntryIsMissing(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	batchID, recordID := seedRecord(ctx, t, pool)
	// current_state says RECOVERED, but the trail stops at RETRYING: exactly
	// the gap the transactional-write rule exists to make impossible
	// (docs/ARCHITECTURE.md section 10a).
	seedRecordState(ctx, t, pool, recordID, "RECORD_STATE_RECOVERED")
	seedAuditEntry(ctx, t, pool, recordID, batchID, "RECORD_STATE_NEW", "RECORD_STATE_SCORING")
	seedAuditEntry(ctx, t, pool, recordID, batchID, "RECORD_STATE_SCORING", "RECORD_STATE_RETRY_SCHEDULED")
	seedAuditEntry(ctx, t, pool, recordID, batchID, "RECORD_STATE_RETRY_SCHEDULED", "RECORD_STATE_RETRYING")

	s := New(pool)
	resp, err := s.GetRecordAudit(ctx, &auditv1.GetRecordAuditRequest{RecordId: recordID})
	if err != nil {
		t.Fatalf("GetRecordAudit: %v", err)
	}
	if resp.TrailComplete {
		t.Error("TrailComplete = true, but current_state is RECOVERED and the trail ends at RETRYING")
	}
}
