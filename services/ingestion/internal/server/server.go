// Package server implements the IngestionService gRPC handler.
//
// Both entry points (SubmitBatch, the demo/backfill path, and SubmitEvent,
// the production webhook path) converge on the same raw.events publish path
// so nothing downstream can tell them apart (docs/ARCHITECTURE.md
// section 0a). The actual SQL and Kafka work live in recordStore
// (store.go) and rawEventPublisher (events.go); this file only orchestrates
// them (docs/ENGINEERING.md section 14).
package server

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/thisizaro/Momotaro/internal/platform/clock"
	"github.com/thisizaro/Momotaro/internal/platform/kafkax"
	"github.com/thisizaro/Momotaro/internal/platform/logger"
	pgxpkg "github.com/thisizaro/Momotaro/internal/platform/pgx"
	ingestionv1 "github.com/thisizaro/Momotaro/proto/gen/ingestion/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Server implements ingestionv1.IngestionServiceServer.
type Server struct {
	ingestionv1.UnimplementedIngestionServiceServer

	store     *recordStore
	publisher *rawEventPublisher
	clock     clock.Clock
}

// New returns a Server publishing to topic (raw.events in production).
// rollingBatchSource names the shared batch SubmitEvent calls attach to
// ("webhook" in production); it is a constructor parameter, the same way
// topic is, so tests can use an isolated value instead of colliding with
// other test runs on the same rolling batch (docs/DECISIONS.md).
func New(pool *pgxpkg.Pool, producer *kafkax.Producer, clk clock.Clock, topic, rollingBatchSource string) *Server {
	return &Server{
		store:     newRecordStore(pool, rollingBatchSource),
		publisher: newRawEventPublisher(producer, topic),
		clock:     clk,
	}
}

// SubmitBatch creates one BATCH row and, for every valid record, a RECORD
// row plus a raw.events publish. Invalid records are reported back by index
// rather than failing the whole batch.
func (s *Server) SubmitBatch(ctx context.Context, req *ingestionv1.SubmitBatchRequest) (*ingestionv1.SubmitBatchResponse, error) {
	if len(req.GetRecords()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "records must not be empty")
	}

	source := req.GetSource()
	if source == "" {
		source = "unknown"
	}

	batchID, err := s.store.createBatch(ctx, source)
	if err != nil {
		return nil, err
	}

	rejected := make(map[int32]string)
	var accepted int32

	for i, nr := range req.GetRecords() {
		if reason := validateNewRecord(nr); reason != "" {
			rejected[int32(i)] = reason
			continue
		}
		if _, err := s.ingestOne(ctx, batchID, nr, ""); err != nil {
			return nil, fmt.Errorf("ingest record %d: %w", i, err)
		}
		accepted++
	}

	if err := s.store.setBatchTotal(ctx, batchID, accepted); err != nil {
		return nil, err
	}

	logger.From(ctx).With(logger.KeyBatchID, batchID).
		Info("batch submitted", "accepted_count", accepted, "rejected_count", len(rejected))

	return &ingestionv1.SubmitBatchResponse{
		BatchId:       batchID,
		AcceptedCount: accepted,
		Rejected:      rejected,
	}, nil
}

// SubmitEvent is the production path: one failure event, as it happens
// (docs/ARCHITECTURE.md section 0a). A repeated idempotency_key is treated
// as the same event: the original record is returned rather than a
// duplicate being created (proto/ingestion/v1/ingestion.proto).
func (s *Server) SubmitEvent(ctx context.Context, req *ingestionv1.SubmitEventRequest) (*ingestionv1.SubmitEventResponse, error) {
	nr := req.GetRecord()
	if nr == nil {
		return nil, status.Error(codes.InvalidArgument, "record is required")
	}

	if key := req.GetIdempotencyKey(); key != "" {
		recordID, batchID, found, err := s.store.findByIdempotencyKey(ctx, key)
		if err != nil {
			return nil, err
		}
		if found {
			logger.ForRecord(logger.From(ctx), recordID, batchID).
				Info("event deduplicated", "idempotency_key", key)
			return &ingestionv1.SubmitEventResponse{RecordId: recordID, BatchId: batchID, Deduplicated: true}, nil
		}
	}

	if reason := validateNewRecord(nr); reason != "" {
		return nil, status.Error(codes.InvalidArgument, reason)
	}

	batchID, err := s.store.rollingBatchID(ctx)
	if err != nil {
		return nil, err
	}

	recordID, err := s.ingestOne(ctx, batchID, nr, req.GetIdempotencyKey())
	if err != nil {
		return nil, err
	}
	if err := s.store.incrementBatchTotal(ctx, batchID); err != nil {
		return nil, err
	}

	return &ingestionv1.SubmitEventResponse{RecordId: recordID, BatchId: batchID, Deduplicated: false}, nil
}

// ingestOne persists one already-validated record and publishes it to
// raw.events. It is the single place both entry points create a record, so
// SubmitBatch and SubmitEvent cannot drift on what "ingesting a record"
// means. idempotencyKey is "" for every SubmitBatch record.
func (s *Server) ingestOne(ctx context.Context, batchID string, nr *ingestionv1.NewRecord, idempotencyKey string) (string, error) {
	recordID := uuid.NewString()
	currency := currencyOrDefault(nr.GetCurrency())
	createdAt := resolveCreatedAt(s.clock, nr)

	if err := s.store.insertRecord(ctx, newRecordParams{
		ID:             recordID,
		BatchID:        batchID,
		Type:           nr.GetType().String(),
		AmountPaise:    nr.GetAmountPaise(),
		Currency:       currency,
		FailureCode:    nr.GetFailureCode(),
		InstrumentRef:  nr.GetInstrumentRef(),
		CreatedAt:      createdAt,
		IdempotencyKey: idempotencyKey,
	}); err != nil {
		return "", err
	}

	if err := s.publisher.Publish(ctx, RawEvent{
		RecordID:      recordID,
		BatchID:       batchID,
		Type:          nr.GetType().String(),
		AmountPaise:   nr.GetAmountPaise(),
		Currency:      currency,
		FailureCode:   nr.GetFailureCode(),
		InstrumentRef: nr.GetInstrumentRef(),
		CreatedAt:     createdAt,
	}); err != nil {
		return "", err
	}

	logger.ForRecord(logger.From(ctx), recordID, batchID).Info("record ingested")
	return recordID, nil
}
