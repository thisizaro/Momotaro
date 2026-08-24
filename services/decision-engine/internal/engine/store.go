package engine

import (
	"context"
	"fmt"
	"time"

	pgxpkg "github.com/thisizaro/Momotaro/internal/platform/pgx"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

// store is Decision Engine's only access point to RECORD_STATE and
// AUDIT_ENTRY, the tables it owns (docs/ARCHITECTURE.md section 10a).
// Keeping every SQL statement behind this type is what lets engine.go and
// scheduler.go read as orchestration rather than a mix of gRPC/Kafka
// handling and SQL (docs/ENGINEERING.md section 14).
type store struct {
	pool *pgxpkg.Pool
}

func newStore(pool *pgxpkg.Pool) *store {
	return &store{pool: pool}
}

// recordStateExists reports whether RECORD_STATE already has a row for
// recordID. HandleMessage uses this to recognise a redelivered raw.events
// message (crash before offset commit, at-least-once delivery) and skip
// reprocessing it, regardless of which state it has already reached.
func (s *store) recordStateExists(ctx context.Context, recordID string) (bool, error) {
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM record_state WHERE record_id = $1)`, recordID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check record_state for %s: %w", recordID, err)
	}
	return exists, nil
}

// scheduleNew creates the first RECORD_STATE row for a freshly classified
// record and its NEW -> state audit entry, in one transaction
// (docs/ARCHITECTURE.md section 10a). attempt_count starts at 0: scheduling
// is not itself an execution attempt.
func (s *store) scheduleNew(ctx context.Context, evt RawEvent, bucket commonv1.RootCauseBucket, state commonv1.RecordState, pendingAction commonv1.ActionType, reason, rationale string, source commonv1.Source, dueAt *time.Time, now time.Time) error {
	return pgxpkg.WithTx(ctx, s.pool, func(ctx context.Context, tx pgxpkg.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO record_state (record_id, current_state, attempt_count, root_cause_bucket, pending_action, due_at, last_action_at, updated_at)
			VALUES ($1, $2, 0, $3, $4, $5, $6, $6)`,
			evt.RecordID, state.String(), bucket.String(), nullIfUnspecified(pendingAction), dueAt, now,
		); err != nil {
			return fmt.Errorf("insert record_state: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO audit_entry (record_id, batch_id, ts, from_state, to_state, reason, rationale, source, actor, attempt_number)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'system', 0)`,
			evt.RecordID, evt.BatchID, now, commonv1.RecordState_RECORD_STATE_NEW.String(), state.String(), reason, rationale, source.String(),
		); err != nil {
			return fmt.Errorf("insert audit_entry: %w", err)
		}
		return nil
	})
}

// claimedRecord is one record the scheduler just claimed: enough to call
// Execute and, afterward, record the outcome without a second round trip.
type claimedRecord struct {
	RecordID      string
	BatchID       string
	FromState     commonv1.RecordState
	ClaimedState  commonv1.RecordState
	PendingAction commonv1.ActionType
	AttemptCount  int // before this attempt; the attempt about to run is AttemptCount+1
	AmountPaise   int64
}

// claimDue finds up to limit records whose due_at has passed and claims
// them: FOR UPDATE OF rs SKIP LOCKED means every Decision Engine pod can
// poll concurrently and safely, each claiming a disjoint set of rows, no
// leader election needed (docs/ARCHITECTURE.md section 7a). The claim
// transition itself (Scheduled -> Retrying/Nudged) and its audit entry are
// written before this returns; Execute happens after, outside this
// transaction, so a slow downstream call never holds these row locks.
func (s *store) claimDue(ctx context.Context, now time.Time, limit int) ([]claimedRecord, error) {
	var claimed []claimedRecord
	err := pgxpkg.WithTx(ctx, s.pool, func(ctx context.Context, tx pgxpkg.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT rs.record_id, r.batch_id, rs.current_state, rs.pending_action, rs.attempt_count, r.amount_paise
			FROM record_state rs
			JOIN record r ON r.id = rs.record_id
			WHERE rs.due_at IS NOT NULL AND rs.due_at <= $1
			  AND rs.current_state IN ($2, $3)
			ORDER BY rs.due_at
			LIMIT $4
			FOR UPDATE OF rs SKIP LOCKED`,
			now,
			commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED.String(),
			commonv1.RecordState_RECORD_STATE_NUDGE_SCHEDULED.String(),
			limit,
		)
		if err != nil {
			return fmt.Errorf("query due records: %w", err)
		}

		type row struct {
			recordID, batchID, currentState, pendingAction string
			attemptCount                                   int
			amountPaise                                    int64
		}
		var scanned []row
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.recordID, &r.batchID, &r.currentState, &r.pendingAction, &r.attemptCount, &r.amountPaise); err != nil {
				rows.Close()
				return fmt.Errorf("scan due record: %w", err)
			}
			scanned = append(scanned, r)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate due records: %w", err)
		}

		for _, r := range scanned {
			fromState := commonv1.RecordState(commonv1.RecordState_value[r.currentState])
			toState := claimedState(fromState)

			if _, err := tx.Exec(ctx, `
				UPDATE record_state SET current_state=$1, due_at=NULL, last_action_at=$2, updated_at=$2 WHERE record_id=$3`,
				toState.String(), now, r.recordID,
			); err != nil {
				return fmt.Errorf("claim record_state for %s: %w", r.recordID, err)
			}
			// No source here: claiming a due record is the scheduler resuming
			// an already-decided action, not a new classification, so there
			// is no LLM/rules provenance to record for this transition.
			if _, err := tx.Exec(ctx, `
				INSERT INTO audit_entry (record_id, batch_id, ts, from_state, to_state, reason, actor, attempt_number)
				VALUES ($1, $2, $3, $4, $5, 'scheduler claimed due record', 'system', $6)`,
				r.recordID, r.batchID, now, fromState.String(), toState.String(), r.attemptCount,
			); err != nil {
				return fmt.Errorf("insert claim audit_entry for %s: %w", r.recordID, err)
			}

			claimed = append(claimed, claimedRecord{
				RecordID:      r.recordID,
				BatchID:       r.batchID,
				FromState:     fromState,
				ClaimedState:  toState,
				PendingAction: commonv1.ActionType(commonv1.ActionType_value[r.pendingAction]),
				AttemptCount:  r.attemptCount,
				AmountPaise:   r.amountPaise,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return claimed, nil
}

// recordOutcome persists the result of executing a claimed record's pending
// action: the new state, the bumped attempt_count, and the audit entry for
// this transition, in one transaction.
func (s *store) recordOutcome(ctx context.Context, c claimedRecord, toState commonv1.RecordState, reason string, attemptNumber int, costPaise int64, now time.Time) error {
	return pgxpkg.WithTx(ctx, s.pool, func(ctx context.Context, tx pgxpkg.Tx) error {
		if _, err := tx.Exec(ctx, `
			UPDATE record_state SET current_state=$1, attempt_count=$2, last_action_at=$3, updated_at=$3 WHERE record_id=$4`,
			toState.String(), attemptNumber, now, c.RecordID,
		); err != nil {
			return fmt.Errorf("update record_state for %s: %w", c.RecordID, err)
		}
		// No source here either: this is Execute's outcome, not a
		// classification, so there is no LLM/rules provenance to record.
		if _, err := tx.Exec(ctx, `
			INSERT INTO audit_entry (record_id, batch_id, ts, from_state, to_state, reason, actor, attempt_number, cost_paise)
			VALUES ($1, $2, $3, $4, $5, $6, 'system', $7, $8)`,
			c.RecordID, c.BatchID, now, c.ClaimedState.String(), toState.String(), reason, attemptNumber, costPaise,
		); err != nil {
			return fmt.Errorf("insert outcome audit_entry for %s: %w", c.RecordID, err)
		}
		return nil
	})
}

// nullIfUnspecified stores ACTION_TYPE_UNSPECIFIED as SQL NULL: an
// escalated-on-classify record has no pending action at all, which is a
// distinct fact from "we forgot to set one."
func nullIfUnspecified(a commonv1.ActionType) any {
	if a == commonv1.ActionType_ACTION_TYPE_UNSPECIFIED {
		return nil
	}
	return a.String()
}

// loadAttemptHistory reads what has already been spent on one record, for the
// guardrails to judge (guardrails.go). The counts are derived here rather than
// stored as columns on RECORD_STATE so they cannot drift from
// INTERVENTION_ATTEMPT, which is the history the audit trail is the source of
// truth for. INTERVENTION_ATTEMPT is the Executor's table and the Decision
// Engine is a permitted reader of it (docs/ARCHITECTURE.md section 10a).
//
// In-flight attempts count, and that is deliberate: the Executor inserts its
// attempt row BEFORE performing the side effect (section 11), so an action
// already under way consumes budget rather than racing a second one.
func (s *store) loadAttemptHistory(ctx context.Context, recordID string) (attemptHistory, error) {
	nudges := []string{
		commonv1.ActionType_ACTION_TYPE_NUDGE_METHOD_UPDATE.String(),
		commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER.String(),
	}

	var h attemptHistory
	// COUNT(ia.id) rather than COUNT(*): the LEFT JOIN yields one all-NULL row
	// for a record with no attempts, and COUNT(*) would score that as 1.
	err := s.pool.QueryRow(ctx, `
		SELECT r.created_at,
		       COUNT(ia.id) FILTER (WHERE ia.action_type = $2),
		       COUNT(ia.id) FILTER (WHERE ia.action_type = ANY($3)),
		       MAX(ia.executed_at) FILTER (WHERE ia.action_type = ANY($3))
		FROM record r
		LEFT JOIN intervention_attempt ia ON ia.record_id = r.id
		WHERE r.id = $1
		GROUP BY r.created_at`,
		recordID, commonv1.ActionType_ACTION_TYPE_RETRY.String(), nudges,
	).Scan(&h.RecordCreatedAt, &h.Retries, &h.Contacts, &h.LastContactAt)
	if err != nil {
		return attemptHistory{}, fmt.Errorf("load attempt history for %s: %w", recordID, err)
	}
	return h, nil
}
