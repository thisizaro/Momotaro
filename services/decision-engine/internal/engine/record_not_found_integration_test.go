//go:build integration

// Store-level coverage for the two places store.go now classifies "this
// record does not exist" as ErrRecordNotFound rather than a plain error
// (docs/INCIDENTS.md 2026-08-31). The end-to-end proof that this actually
// keeps the consumer alive is
// TestConsumeKeyedDeadLettersMissingRecordAndKeepsGoing
// (poison_record_integration_test.go); these two are the narrower,
// faster-to-read proof that each store method draws the right line.
package engine

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	"github.com/thisizaro/Momotaro/services/decision-engine/internal/economics"
)

// TestLoadAttemptHistoryReturnsErrRecordNotFoundForMissingRecord is the
// exact query the incident's stack trace names
// ("load attempt history for 10187b3b-...: no rows in result set"): a
// record_id with no RECORD row must come back as ErrRecordNotFound, not a
// bare error indistinguishable from a database problem.
func TestLoadAttemptHistoryReturnsErrRecordNotFoundForMissingRecord(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	missingRecordID := uuid.NewString() // never inserted into record

	_, err := newStore(pool).loadAttemptHistory(ctx, missingRecordID)
	if err == nil {
		t.Fatal("loadAttemptHistory returned no error for a record_id that does not exist")
	}
	if !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("err = %v, want it to wrap ErrRecordNotFound", err)
	}
}

// TestScheduleNewReturnsErrRecordNotFoundWhenRecordMissing covers the
// narrower race found while auditing the rest of HandleMessage's path per
// this unit's brief ("nothing guarantees loadAttemptHistory is the only
// one"): a record deleted between loadAttemptHistory succeeding and this
// INSERT running (both happen inside one HandleMessage call, with a real
// Classify RPC round trip in between) hits record_state's foreign key
// instead, and that must be classified exactly the same way, not left as a
// generic error that would also stop ConsumeKeyed's loop.
func TestScheduleNewReturnsErrRecordNotFoundWhenRecordMissing(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	missingRecordID := uuid.NewString() // never inserted into record

	evt := RawEvent{
		RecordID:    missingRecordID,
		BatchID:     uuid.NewString(),
		Type:        "RECORD_TYPE_PAYMENT",
		AmountPaise: 10000,
		FailureCode: "BANK_TIMEOUT",
		CreatedAt:   time.Now(),
	}
	steps := directPath(commonv1.RecordState_RECORD_STATE_ESCALATED, "test: record does not exist")

	err := newStore(pool).scheduleNew(ctx, slog.Default(), evt, commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK, steps, commonv1.ActionType_ACTION_TYPE_UNSPECIFIED, "test rationale", commonv1.Source_SOURCE_RULES_FALLBACK, nil, economics.Score{}, DecisionTrace{}, nil, time.Now())
	if err == nil {
		t.Fatal("scheduleNew returned no error inserting record_state for a record_id that does not exist")
	}
	if !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("err = %v, want it to wrap ErrRecordNotFound (a foreign-key violation on record_state)", err)
	}
}
