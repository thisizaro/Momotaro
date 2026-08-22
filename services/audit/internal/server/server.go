// Package server implements the AuditService gRPC handlers.
//
// GetRecordAudit reads straight from Postgres (docs/ARCHITECTURE.md section
// 8: Audit is not a Kafka consumer, since audit rows are written
// transactionally by the owning service, Postgres is already the single
// source of truth). VerifyInvariants, the continuous invariant verifier, is
// out of scope for the walking skeleton (docs/PLAN.md).
package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	pgxpkg "github.com/thisizaro/Momotaro/internal/platform/pgx"
	auditv1 "github.com/thisizaro/Momotaro/proto/gen/audit/v1"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Server implements auditv1.AuditServiceServer.
type Server struct {
	auditv1.UnimplementedAuditServiceServer

	pool *pgxpkg.Pool
}

// New returns a Server reading from pool.
func New(pool *pgxpkg.Pool) *Server {
	return &Server{pool: pool}
}

// GetRecordAudit returns the record, its current state, and its full
// ordered audit trail.
func (s *Server) GetRecordAudit(ctx context.Context, req *auditv1.GetRecordAuditRequest) (*auditv1.GetRecordAuditResponse, error) {
	if req.GetRecordId() == "" {
		return nil, status.Error(codes.InvalidArgument, "record_id is required")
	}

	rec, err := s.loadRecord(ctx, req.GetRecordId())
	if err != nil {
		return nil, err
	}

	currentState, err := s.loadCurrentState(ctx, req.GetRecordId())
	if err != nil {
		return nil, err
	}

	entries, err := s.loadAuditEntries(ctx, req.GetRecordId())
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

func (s *Server) loadRecord(ctx context.Context, recordID string) (*commonv1.Record, error) {
	var rec commonv1.Record
	var typeStr string
	var instrumentRef sql.NullString
	var createdAt time.Time

	err := s.pool.QueryRow(ctx, `
		SELECT id, batch_id, type, amount_paise, currency, failure_code, instrument_ref, created_at
		FROM record WHERE id = $1`, recordID,
	).Scan(&rec.Id, &rec.BatchId, &typeStr, &rec.AmountPaise, &rec.Currency, &rec.FailureCode, &instrumentRef, &createdAt)
	if err != nil {
		if errors.Is(err, pgxpkg.ErrNoRows) {
			return nil, status.Errorf(codes.NotFound, "record %s not found", recordID)
		}
		return nil, fmt.Errorf("query record %s: %w", recordID, err)
	}

	rec.Type = commonv1.RecordType(commonv1.RecordType_value[typeStr])
	rec.CreatedAt = timestamppb.New(createdAt)
	if instrumentRef.Valid {
		rec.InstrumentRef = instrumentRef.String
	}
	return &rec, nil
}

func (s *Server) loadCurrentState(ctx context.Context, recordID string) (commonv1.RecordState, error) {
	var stateStr string
	err := s.pool.QueryRow(ctx, `SELECT current_state FROM record_state WHERE record_id = $1`, recordID).Scan(&stateStr)
	if err != nil {
		if errors.Is(err, pgxpkg.ErrNoRows) {
			// Ingested but not yet touched by the Decision Engine.
			return commonv1.RecordState_RECORD_STATE_UNSPECIFIED, nil
		}
		return commonv1.RecordState_RECORD_STATE_UNSPECIFIED, fmt.Errorf("query record_state for %s: %w", recordID, err)
	}
	return commonv1.RecordState(commonv1.RecordState_value[stateStr]), nil
}

func (s *Server) loadAuditEntries(ctx context.Context, recordID string) ([]*auditv1.AuditEntry, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT ts, from_state, to_state, reason, rationale, source, actor, attempt_number, cost_paise, message_text
		FROM audit_entry WHERE record_id = $1 ORDER BY ts ASC, id ASC`, recordID)
	if err != nil {
		return nil, fmt.Errorf("query audit_entry for %s: %w", recordID, err)
	}
	defer rows.Close()

	var entries []*auditv1.AuditEntry
	for rows.Next() {
		var ts time.Time
		var fromState, toState, reason string
		var rationale, source, actor, messageText sql.NullString
		var attemptNumber sql.NullInt32
		var costPaise sql.NullInt64

		if err := rows.Scan(&ts, &fromState, &toState, &reason, &rationale, &source, &actor, &attemptNumber, &costPaise, &messageText); err != nil {
			return nil, fmt.Errorf("scan audit_entry for %s: %w", recordID, err)
		}

		entries = append(entries, &auditv1.AuditEntry{
			Ts:            timestamppb.New(ts),
			FromState:     commonv1.RecordState(commonv1.RecordState_value[fromState]),
			ToState:       commonv1.RecordState(commonv1.RecordState_value[toState]),
			Reason:        reason,
			Rationale:     rationale.String,
			Source:        commonv1.Source(commonv1.Source_value[source.String]),
			Actor:         actor.String,
			AttemptNumber: attemptNumber.Int32,
			CostPaise:     costPaise.Int64,
			MessageText:   messageText.String,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate audit_entry for %s: %w", recordID, err)
	}
	return entries, nil
}

// VerifyInvariants, the continuous correctness-invariant verifier, is out of
// scope for the walking skeleton (docs/PLAN.md Phase 1/2).
func (s *Server) VerifyInvariants(ctx context.Context, req *auditv1.VerifyInvariantsRequest) (*auditv1.VerifyInvariantsResponse, error) {
	return nil, status.Error(codes.Unimplemented, "VerifyInvariants: out of scope for the walking skeleton")
}
