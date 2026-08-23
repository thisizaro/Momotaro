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
	if _, err := pool.Exec(ctx, `
		INSERT INTO audit_entry (record_id, batch_id, ts, from_state, to_state, reason, rationale, source, actor, attempt_number, cost_paise)
		VALUES
		 ($1, $2, now() - interval '1 minute', 'RECORD_STATE_UNSPECIFIED', 'RECORD_STATE_NEW', 'ingested', NULL, 'SOURCE_UNSPECIFIED', 'system', NULL, NULL),
		 ($1, $2, now(), 'RECORD_STATE_NEW', 'RECORD_STATE_RECOVERED', 'classified and executed', 'transient bank failure, retry', 'SOURCE_RULES_FALLBACK', 'system', 1, 0)
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
	if len(resp.Entries) != 2 {
		t.Fatalf("len(Entries) = %d, want 2", len(resp.Entries))
	}

	// Oldest first.
	if resp.Entries[0].ToState != commonv1.RecordState_RECORD_STATE_NEW {
		t.Errorf("Entries[0].ToState = %v, want RECORD_STATE_NEW", resp.Entries[0].ToState)
	}
	if resp.Entries[1].ToState != commonv1.RecordState_RECORD_STATE_RECOVERED {
		t.Errorf("Entries[1].ToState = %v, want RECORD_STATE_RECOVERED", resp.Entries[1].ToState)
	}
	if resp.Entries[1].Rationale != "transient bank failure, retry" {
		t.Errorf("Entries[1].Rationale = %q, want the classifier rationale", resp.Entries[1].Rationale)
	}
	if resp.Entries[1].Source != commonv1.Source_SOURCE_RULES_FALLBACK {
		t.Errorf("Entries[1].Source = %v, want SOURCE_RULES_FALLBACK", resp.Entries[1].Source)
	}
	if resp.Entries[1].AttemptNumber != 1 {
		t.Errorf("Entries[1].AttemptNumber = %d, want 1", resp.Entries[1].AttemptNumber)
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
