// Package attempt owns INTERVENTION_ATTEMPT, the only table the Executor
// writes (docs/ARCHITECTURE.md section 10a), and with it the durable
// idempotency guarantee.
//
// The shape is insert-before-execute (docs/ARCHITECTURE.md section 11): claim
// the UNIQUE (record_id, attempt_number) slot first, perform the side effect
// only if the claim succeeded, then record what happened. A duplicate loses
// the insert race and is answered from the row that already exists, so the
// action never runs twice however many times the RPC is retried or the Kafka
// message redelivered. Unlike a Redis key this guarantee has no expiry.
package attempt

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	pgxpkg "github.com/thisizaro/Momotaro/internal/platform/pgx"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

// Postgres SQLSTATEs we act on by meaning rather than by message text.
const (
	uniqueViolation     = "23505"
	foreignKeyViolation = "23503"
)

// ErrUnknownRecord means record_id has no RECORD row, so there is nothing to
// attempt an intervention on. Surfaced separately because "you sent me an id
// that does not exist" is a caller error, not an internal failure.
var ErrUnknownRecord = errors.New("record does not exist")

// claimMarker is what `outcome` holds between claiming the slot and knowing
// the answer. The column is NOT NULL, so the row cannot be inserted without
// some value, and the enum's zero value is the honest one: common.proto's
// convention is that _UNSPECIFIED means "unset", distinguishable from any
// deliberately-set value.
//
// It used to be OUTCOME_PENDING, which was fine only while no real outcome
// was ever PENDING. Now that a nudge legitimately resolves to PENDING (Phase
// 5 answers it via ReportDelayedOutcome), reusing PENDING as the marker would
// make "still working" and "sent, awaiting the customer" indistinguishable: a
// redelivered nudge would poll for an answer that is not coming until Phase
// 5, time out, and be dead-lettered despite having executed perfectly.
// See docs/INCIDENTS.md and services/executor/SPEC.md section 4.3.
var claimMarker = commonv1.Outcome_OUTCOME_UNSPECIFIED.String()

// Store is the Executor's access point to INTERVENTION_ATTEMPT.
type Store struct {
	pool *pgxpkg.Pool
}

func NewStore(pool *pgxpkg.Pool) *Store {
	return &Store{pool: pool}
}

// Recorded is a previously completed attempt, replayed to a duplicate caller.
type Recorded struct {
	Outcome     commonv1.Outcome
	CostPaise   int64
	FailureCode string
	ResolvesAt  *time.Time
}

// Claim reserves the (recordID, attemptNumber) slot before any side effect
// runs, returning the new row's id. claimed is false when the slot was
// already taken, which is the duplicate-delivery case and not an error.
//
// evScoreAtDecision and pRecoveryAtDecision are the Decision Engine's
// economics decision snapshot for this action, recorded as-is: the Executor
// never scores, it only persists what it was told
// (docs/PHASE2_IMPLEMENTATION.md Unit G).
func (s *Store) Claim(ctx context.Context, recordID string, attemptNumber int32, action commonv1.ActionType, message string, evScoreAtDecision, pRecoveryAtDecision float64, now time.Time) (id string, claimed bool, err error) {
	id = uuid.NewString()
	_, err = s.pool.Exec(ctx, `
		INSERT INTO intervention_attempt
			(id, record_id, attempt_number, action_type, outcome, executed_at, cost_paise, message_text, failure_code, ev_score_at_decision, p_recovery_at_decision)
		VALUES ($1, $2, $3, $4, $5, $6, 0, $7, '', $8, $9)`,
		id, recordID, attemptNumber, action.String(), claimMarker, now, message, evScoreAtDecision, pRecoveryAtDecision)
	if err == nil {
		return id, true, nil
	}
	switch {
	case isSQLState(err, uniqueViolation):
		return "", false, nil
	case isSQLState(err, foreignKeyViolation):
		// record_id references record(id), so this is an unknown record
		// rather than anything wrong on our side.
		return "", false, fmt.Errorf("%w: %s", ErrUnknownRecord, recordID)
	default:
		return "", false, fmt.Errorf("claim attempt %d for %s: %w", attemptNumber, recordID, err)
	}
}

// RecordOutcome fills in what the action actually did, replacing the claim
// marker. resolvesAt is nil unless the outcome is PENDING.
func (s *Store) RecordOutcome(ctx context.Context, id string, outcome commonv1.Outcome, costPaise int64, failureCode string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE intervention_attempt SET outcome=$1, cost_paise=$2, failure_code=$3 WHERE id=$4`,
		outcome.String(), costPaise, failureCode, id)
	if err != nil {
		return fmt.Errorf("record outcome for attempt %s: %w", id, err)
	}
	return nil
}

// Load reads the recorded attempt for (recordID, attemptNumber). resolved is
// false while the row still holds the claim marker, meaning the original
// caller has claimed the slot but has not finished yet.
func (s *Store) Load(ctx context.Context, recordID string, attemptNumber int32) (rec Recorded, resolved bool, err error) {
	var outcomeStr, failureCode string
	var costPaise int64
	err = s.pool.QueryRow(ctx, `
		SELECT outcome, cost_paise, coalesce(failure_code, '')
		FROM intervention_attempt WHERE record_id=$1 AND attempt_number=$2`,
		recordID, attemptNumber).Scan(&outcomeStr, &costPaise, &failureCode)
	if err != nil {
		return Recorded{}, false, fmt.Errorf("load attempt %d for %s: %w", attemptNumber, recordID, err)
	}
	if outcomeStr == claimMarker {
		return Recorded{}, false, nil
	}
	return Recorded{
		Outcome:     commonv1.Outcome(commonv1.Outcome_value[outcomeStr]),
		CostPaise:   costPaise,
		FailureCode: failureCode,
	}, true, nil
}

func isSQLState(err error, code string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code
}
