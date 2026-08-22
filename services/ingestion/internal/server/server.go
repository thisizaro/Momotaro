// Package server implements the IngestionService gRPC handler.
//
// SubmitBatch is the only entry point built here (docs/PLAN.md walking
// skeleton); SubmitEvent (the production one-event-at-a-time webhook path,
// docs/ARCHITECTURE.md section 0a) converges on the same raw.events publish
// once it lands.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/thisizaro/Momotaro/internal/platform/clock"
	"github.com/thisizaro/Momotaro/internal/platform/kafkax"
	"github.com/thisizaro/Momotaro/internal/platform/logger"
	pgxpkg "github.com/thisizaro/Momotaro/internal/platform/pgx"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	ingestionv1 "github.com/thisizaro/Momotaro/proto/gen/ingestion/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// RawEvent is the raw.events wire payload. There is no proto for this: the
// topic sits inside the cluster only, between Ingestion (producer) and
// Decision Engine (consumer), and docs/ARCHITECTURE.md section 9's proto
// discipline applies to gRPC contracts, not internal topic payloads.
//
// The mirror of this type lives in
// services/decision-engine/internal/consumer; the two must be kept
// structurally in sync by hand until this is promoted to a real contract.
type RawEvent struct {
	RecordID      string    `json:"record_id"`
	BatchID       string    `json:"batch_id"`
	Type          string    `json:"type"`
	AmountPaise   int64     `json:"amount_paise"`
	Currency      string    `json:"currency"`
	FailureCode   string    `json:"failure_code"`
	InstrumentRef string    `json:"instrument_ref"`
	CreatedAt     time.Time `json:"created_at"`
}

// Server implements ingestionv1.IngestionServiceServer.
type Server struct {
	ingestionv1.UnimplementedIngestionServiceServer

	pool     *pgxpkg.Pool
	producer *kafkax.Producer
	clock    clock.Clock
	topic    string
}

// New returns a Server publishing to topic (raw.events in production).
func New(pool *pgxpkg.Pool, producer *kafkax.Producer, clk clock.Clock, topic string) *Server {
	return &Server{pool: pool, producer: producer, clock: clk, topic: topic}
}

// SubmitBatch creates one BATCH row and, for every valid record, a RECORD
// row plus a raw.events publish keyed by the new record_id. Invalid records
// are reported back by index rather than failing the whole batch.
func (s *Server) SubmitBatch(ctx context.Context, req *ingestionv1.SubmitBatchRequest) (*ingestionv1.SubmitBatchResponse, error) {
	if len(req.GetRecords()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "records must not be empty")
	}

	source := req.GetSource()
	if source == "" {
		source = "unknown"
	}

	batchID := uuid.NewString()
	if _, err := s.pool.Exec(ctx, `INSERT INTO batch (id, source, total_records) VALUES ($1, $2, 0)`, batchID, source); err != nil {
		return nil, fmt.Errorf("create batch: %w", err)
	}

	log := logger.From(ctx).With(logger.KeyBatchID, batchID)

	rejected := make(map[int32]string)
	accepted := int32(0)

	for i, nr := range req.GetRecords() {
		if reason := validateNewRecord(nr); reason != "" {
			rejected[int32(i)] = reason
			continue
		}

		recordID := uuid.NewString()
		currency := nr.GetCurrency()
		if currency == "" {
			currency = "INR"
		}
		createdAt := s.clock.Now()
		if nr.GetOccurredAt() != nil {
			createdAt = nr.GetOccurredAt().AsTime()
		}

		if _, err := s.pool.Exec(ctx, `
			INSERT INTO record (id, batch_id, type, amount_paise, currency, failure_code, instrument_ref, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			recordID, batchID, nr.GetType().String(), nr.GetAmountPaise(), currency,
			nr.GetFailureCode(), nr.GetInstrumentRef(), createdAt); err != nil {
			return nil, fmt.Errorf("insert record %d: %w", i, err)
		}

		payload, err := json.Marshal(RawEvent{
			RecordID:      recordID,
			BatchID:       batchID,
			Type:          nr.GetType().String(),
			AmountPaise:   nr.GetAmountPaise(),
			Currency:      currency,
			FailureCode:   nr.GetFailureCode(),
			InstrumentRef: nr.GetInstrumentRef(),
			CreatedAt:     createdAt,
		})
		if err != nil {
			return nil, fmt.Errorf("marshal raw event for record %s: %w", recordID, err)
		}

		if err := s.producer.Publish(ctx, s.topic, recordID, payload); err != nil {
			return nil, fmt.Errorf("publish raw event for record %s: %w", recordID, err)
		}

		logger.ForRecord(log, recordID, batchID).Info("record ingested")
		accepted++
	}

	if _, err := s.pool.Exec(ctx, `UPDATE batch SET total_records=$1 WHERE id=$2`, accepted, batchID); err != nil {
		return nil, fmt.Errorf("update batch total_records: %w", err)
	}

	log.Info("batch submitted", "accepted_count", accepted, "rejected_count", len(rejected))

	return &ingestionv1.SubmitBatchResponse{
		BatchId:       batchID,
		AcceptedCount: accepted,
		Rejected:      rejected,
	}, nil
}

// SubmitEvent, the production webhook path, is out of scope for the walking
// skeleton (docs/PLAN.md Phase 1).
func (s *Server) SubmitEvent(ctx context.Context, req *ingestionv1.SubmitEventRequest) (*ingestionv1.SubmitEventResponse, error) {
	return nil, status.Error(codes.Unimplemented, "SubmitEvent: out of scope for the walking skeleton")
}

// validateNewRecord returns a human-readable rejection reason, or "" if the
// record is acceptable. Amount must be positive to satisfy record's own
// CHECK constraint without the caller ever seeing a raw SQL error.
func validateNewRecord(nr *ingestionv1.NewRecord) string {
	if nr.GetType() == commonv1.RecordType_RECORD_TYPE_UNSPECIFIED {
		return "type is required"
	}
	if nr.GetAmountPaise() <= 0 {
		return "amount_paise must be positive"
	}
	if nr.GetFailureCode() == "" {
		return "failure_code is required"
	}
	return ""
}
