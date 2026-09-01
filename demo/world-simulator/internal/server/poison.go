package server

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	worldsimv1 "github.com/thisizaro/Momotaro/proto/gen/worldsim/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// poisonFailureCode is a real, published Razorpay failure code
// (services/classifier/internal/rules/buckets.go), used only as plausible
// payload content. InjectPoison's whole demonstration is a record_id with
// no RECORD row, not an unrecognised failure code, so the code itself must
// not also be part of what looks broken.
const poisonFailureCode = "BANK_TIMEOUT"

// poisonAmountPaise is a placeholder amount (Rs 100). It is never charged:
// this message is designed to never reach a RECORD row at all.
const poisonAmountPaise int64 = 10000

// InjectPoison publishes one raw.events message referencing a record_id
// that was never inserted into RECORD, to demonstrate the dead-letter path
// live from the dashboard (docs/PHASE5_5_IMPLEMENTATION.md Unit U fixed the
// consumer side; this unit, W, is what lets a judge trigger it without a
// shell). Backs POST /v1/demo/inject-poison.
func (s *Server) InjectPoison(ctx context.Context, req *worldsimv1.InjectPoisonRequest) (*worldsimv1.InjectPoisonResponse, error) {
	if s.producer == nil || s.rawEventsTopic == "" {
		return nil, status.Error(codes.FailedPrecondition, "world simulator has no raw.events producer configured")
	}

	recordID := uuid.NewString()
	batchID := uuid.NewString()

	payload, err := json.Marshal(rawEvent{
		RecordID:    recordID,
		BatchID:     batchID,
		Type:        commonv1.RecordType_RECORD_TYPE_PAYMENT.String(),
		AmountPaise: poisonAmountPaise,
		Currency:    "INR",
		FailureCode: poisonFailureCode,
		CreatedAt:   s.clock.Now(),
	})
	if err != nil {
		return nil, fmt.Errorf("marshal poison raw event: %w", err)
	}
	if err := s.producer.Publish(ctx, s.rawEventsTopic, recordID, payload); err != nil {
		return nil, fmt.Errorf("publish poison raw event: %w", err)
	}

	return &worldsimv1.InjectPoisonResponse{RecordId: recordID, BatchId: batchID}, nil
}
