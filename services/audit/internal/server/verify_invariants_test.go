//go:build integration

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

func TestVerifyInvariantsRejectsMalformedBatchID(t *testing.T) {
	s := New(testPool(t))

	_, err := s.VerifyInvariants(context.Background(), &auditv1.VerifyInvariantsRequest{BatchId: "not-a-uuid"})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("VerifyInvariants with a malformed batch_id: err = %v, want InvalidArgument", err)
	}
}

func TestVerifyInvariantsEmptyBatchIDChecksEverythingWithoutErroring(t *testing.T) {
	// Regression: an empty batch_id must not reach the SQL layer's
	// ::uuid cast unguarded. See docs/INCIDENTS.md.
	s := New(testPool(t))

	if _, err := s.VerifyInvariants(context.Background(), &auditv1.VerifyInvariantsRequest{}); err != nil {
		t.Fatalf("VerifyInvariants with empty batch_id: %v", err)
	}
}

func TestVerifyInvariantsOnACleanRecordReportsNoViolations(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	batchID, recordID := seedRecord(ctx, t, pool)
	seedRecordState(ctx, t, pool, recordID, commonv1.RecordState_RECORD_STATE_ESCALATED.String())
	seedAuditEntry(ctx, t, pool, recordID, batchID,
		commonv1.RecordState_RECORD_STATE_NEW.String(), commonv1.RecordState_RECORD_STATE_ESCALATED.String())

	s := New(pool)
	resp, err := s.VerifyInvariants(ctx, &auditv1.VerifyInvariantsRequest{BatchId: batchID})
	if err != nil {
		t.Fatalf("VerifyInvariants: %v", err)
	}

	if resp.RecordsChecked != 1 {
		t.Errorf("RecordsChecked = %d, want 1", resp.RecordsChecked)
	}
	if resp.IncompleteAuditTrails != 0 || resp.ImpossibleTransitions != 0 || resp.StoppingRuleViolations != 0 {
		t.Errorf("expected zero violations, got %+v", resp)
	}
}

func TestVerifyInvariantsCatchesARealIncompleteTrail(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	batchID, recordID := seedRecord(ctx, t, pool)
	// record_state exists, but no audit_entry was ever written for it: the
	// exact bug the transactional-write rule (docs/ARCHITECTURE.md section
	// 10a) exists to make impossible.
	seedRecordState(ctx, t, pool, recordID, commonv1.RecordState_RECORD_STATE_RECOVERED.String())

	s := New(pool)
	resp, err := s.VerifyInvariants(ctx, &auditv1.VerifyInvariantsRequest{BatchId: batchID})
	if err != nil {
		t.Fatalf("VerifyInvariants: %v", err)
	}

	if resp.IncompleteAuditTrails != 1 {
		t.Errorf("IncompleteAuditTrails = %d, want 1", resp.IncompleteAuditTrails)
	}
	if _, ok := resp.Examples[recordID]; !ok {
		t.Errorf("no example recorded for %s", recordID)
	}
}

func TestVerifyInvariantsScopesToOneBatch(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	batchA, recordA := seedRecord(ctx, t, pool)
	_, recordB := seedRecord(ctx, t, pool)
	// Both broken, but only recordA's batch is queried.
	seedRecordState(ctx, t, pool, recordA, commonv1.RecordState_RECORD_STATE_RECOVERED.String())
	seedRecordState(ctx, t, pool, recordB, commonv1.RecordState_RECORD_STATE_RECOVERED.String())

	s := New(pool)
	resp, err := s.VerifyInvariants(ctx, &auditv1.VerifyInvariantsRequest{BatchId: batchA})
	if err != nil {
		t.Fatalf("VerifyInvariants: %v", err)
	}

	if resp.RecordsChecked != 1 {
		t.Errorf("RecordsChecked = %d, want 1 (scoped to one batch)", resp.RecordsChecked)
	}
	if resp.IncompleteAuditTrails != 1 {
		t.Errorf("IncompleteAuditTrails = %d, want 1", resp.IncompleteAuditTrails)
	}
	if _, ok := resp.Examples[recordB]; ok {
		t.Error("a record from a different batch leaked into the examples")
	}
}

func TestVerifyInvariantsUnknownBatchIDReportsZeroRecordsNotAnError(t *testing.T) {
	s := New(testPool(t))

	resp, err := s.VerifyInvariants(context.Background(), &auditv1.VerifyInvariantsRequest{BatchId: uuid.NewString()})
	if err != nil {
		t.Fatalf("VerifyInvariants for an unknown (but valid) batch_id: %v", err)
	}
	if resp.RecordsChecked != 0 {
		t.Errorf("RecordsChecked = %d, want 0", resp.RecordsChecked)
	}
}
