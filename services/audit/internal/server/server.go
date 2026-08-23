// Package server implements the AuditService gRPC handlers.
//
// Audit does not write audit rows: the owning service writes RECORD_STATE
// and its AUDIT_ENTRY transactionally (docs/ARCHITECTURE.md section 10a).
// This service only reads Postgres directly (section 8: Audit is not a
// Kafka consumer, since Postgres is already the single source of truth),
// and does two things with what it reads: serve one record's trail
// (GetRecordAudit) and continuously check the system's own correctness
// invariants (VerifyInvariants). All SQL lives in store.go
// (recordSnapshot, scanRecords); the state machine's valid edges live in
// statemachine.go; the aggregation logic lives in verify.go. This file is
// orchestration only (docs/ENGINEERING.md section 14).
package server

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	pgxpkg "github.com/thisizaro/Momotaro/internal/platform/pgx"
	auditv1 "github.com/thisizaro/Momotaro/proto/gen/audit/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Server implements auditv1.AuditServiceServer.
type Server struct {
	auditv1.UnimplementedAuditServiceServer

	store *store
}

// New returns a Server reading from pool.
func New(pool *pgxpkg.Pool) *Server {
	return &Server{store: newStore(pool)}
}

// GetRecordAudit returns the record, its current state, and its full
// ordered audit trail.
func (s *Server) GetRecordAudit(ctx context.Context, req *auditv1.GetRecordAuditRequest) (*auditv1.GetRecordAuditResponse, error) {
	if req.GetRecordId() == "" {
		return nil, status.Error(codes.InvalidArgument, "record_id is required")
	}

	rec, err := s.store.loadRecord(ctx, req.GetRecordId())
	if err != nil {
		if errors.Is(err, pgxpkg.ErrNoRows) {
			return nil, status.Errorf(codes.NotFound, "record %s not found", req.GetRecordId())
		}
		return nil, fmt.Errorf("load record %s: %w", req.GetRecordId(), err)
	}

	currentState, err := s.store.loadCurrentState(ctx, req.GetRecordId())
	if err != nil {
		return nil, err
	}

	entries, err := s.store.loadAuditEntries(ctx, req.GetRecordId())
	if err != nil {
		return nil, err
	}

	return &auditv1.GetRecordAuditResponse{
		Record:        rec,
		CurrentState:  currentState,
		Entries:       entries,
		TrailComplete: true,
	}, nil
}

// VerifyInvariants scans the requested scope (one batch, or everything when
// batch_id is empty) and reports the correctness invariants from
// docs/ARCHITECTURE.md section 10a. Every count in the response should be
// zero; a nonzero one is a bug, not a business outcome, and is what
// justifies Audit's "continuously verifies" half of its job (see
// services/audit/AGENTS.md).
func (s *Server) VerifyInvariants(ctx context.Context, req *auditv1.VerifyInvariantsRequest) (*auditv1.VerifyInvariantsResponse, error) {
	batchID := req.GetBatchId()
	if batchID != "" {
		if _, err := uuid.Parse(batchID); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "batch_id %q is not a valid UUID", batchID)
		}
	}

	snapshots, err := s.store.scanRecords(ctx, batchID)
	if err != nil {
		return nil, fmt.Errorf("scan records for batch %q: %w", batchID, err)
	}

	return verifyInvariants(snapshots), nil
}
