package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/thisizaro/Momotaro/internal/platform/hopcodec"
	"github.com/thisizaro/Momotaro/internal/platform/logger"
	pgxpkg "github.com/thisizaro/Momotaro/internal/platform/pgx"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	"github.com/thisizaro/Momotaro/services/decision-engine/internal/economics"
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
// is not itself an execution attempt. score is the economics scorer's
// winning candidate, persisted so Execute (which happens later, when the
// scheduler claims this record) can learn what was decided
// (docs/PHASE2_IMPLEMENTATION.md Unit G); it is the zero value when the
// record never reached scoring (an explicit escalation), in which case
// nothing is stored rather than a misleading zero.
func (s *store) scheduleNew(ctx context.Context, log *slog.Logger, evt RawEvent, bucket commonv1.RootCauseBucket, steps []stateStep, pendingAction commonv1.ActionType, rationale string, source commonv1.Source, hops []*commonv1.ProviderHop, score economics.Score, dueAt *time.Time, now time.Time) error {
	if len(steps) == 0 {
		return fmt.Errorf("schedule %s: no transitions to record", evt.RecordID)
	}
	final := steps[len(steps)-1].To
	evScore, pRecovery := scoreColumns(score)

	// Which provider rungs were actually tried, so the trail can show a
	// fallback rather than only the coarse Source (Phase 3 Unit E). An
	// encoding failure must not lose the record: the hops are diagnostic, the
	// classification is not, so log and store NULL rather than failing the
	// whole transaction.
	hopsCol := encodedHops(hops, log)

	return pgxpkg.WithTx(ctx, s.pool, func(ctx context.Context, tx pgxpkg.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO record_state (record_id, current_state, attempt_count, root_cause_bucket, pending_action, due_at, ev_score_at_decision, p_recovery_at_decision, last_action_at, updated_at)
			VALUES ($1, $2, 0, $3, $4, $5, $6, $7, $8, $8)`,
			evt.RecordID, final.String(), bucket.String(), nullIfUnspecified(pendingAction), dueAt, evScore, pRecovery, now,
		); err != nil {
			return fmt.Errorf("insert record_state: %w", err)
		}
		// Every step gets its own audit row, in the same transaction as the
		// state change, so there is no window in which a record moved without
		// a record of why (docs/ARCHITECTURE.md section 10a).
		for _, step := range steps {
			if _, err := tx.Exec(ctx, `
				INSERT INTO audit_entry (record_id, batch_id, ts, from_state, to_state, reason, rationale, source, provider_hops, actor, attempt_number)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'system', 0)`,
				evt.RecordID, evt.BatchID, now, step.From.String(), step.To.String(), step.Reason, rationale, source.String(), hopsCol,
			); err != nil {
				return fmt.Errorf("insert audit_entry %s -> %s: %w", step.From, step.To, err)
			}
		}
		return nil
	})
}

// claimedRecord is one record the scheduler just claimed: enough to call
// Execute and, afterward, record the outcome without a second round trip.
type claimedRecord struct {
	RecordID        string
	BatchID         string
	FromState       commonv1.RecordState
	ClaimedState    commonv1.RecordState
	PendingAction   commonv1.ActionType
	AttemptCount    int // before this attempt; the attempt about to run is AttemptCount+1
	AmountPaise     int64
	RootCauseBucket commonv1.RootCauseBucket // needed to re-score a failed attempt without re-classifying
	// Type is the record's RECORD_TYPE, needed by retryDueAt's mandate
	// lead-time floor (docs/PHASE5_IMPLEMENTATION.md Unit J): only a
	// RECORD_TYPE_MANDATE retry carries the RBI pre-debit lead time.
	Type commonv1.RecordType
	// EVScoreAtDecision and PRecoveryAtDecision are the scorer's decision
	// snapshot from when this record was last scheduled or re-scored, to
	// forward on the Execute call (docs/PHASE2_IMPLEMENTATION.md Unit G).
	// Only records in RETRY_SCHEDULED/NUDGE_SCHEDULED are ever claimed, and
	// scoreAndRoute only routes there with a positive-EV winning score, so a
	// claimed record always has one; zero here would mean a data bug in an
	// older row, not "unscored", and is never written by this service.
	EVScoreAtDecision   float64
	PRecoveryAtDecision float64
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
			SELECT rs.record_id, r.batch_id, rs.current_state, rs.pending_action, rs.attempt_count, r.amount_paise, rs.root_cause_bucket, rs.ev_score_at_decision, rs.p_recovery_at_decision, r.type
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
			rootCauseBucket                                *string
			evScoreAtDecision, pRecoveryAtDecision         *float64
			recordType                                     string
		}
		var scanned []row
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.recordID, &r.batchID, &r.currentState, &r.pendingAction, &r.attemptCount, &r.amountPaise, &r.rootCauseBucket, &r.evScoreAtDecision, &r.pRecoveryAtDecision, &r.recordType); err != nil {
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

			bucket := commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_UNSPECIFIED
			if r.rootCauseBucket != nil {
				bucket = commonv1.RootCauseBucket(commonv1.RootCauseBucket_value[*r.rootCauseBucket])
			}
			var evScore, pRecovery float64
			if r.evScoreAtDecision != nil {
				evScore = *r.evScoreAtDecision
			}
			if r.pRecoveryAtDecision != nil {
				pRecovery = *r.pRecoveryAtDecision
			}
			claimed = append(claimed, claimedRecord{
				RecordID:            r.recordID,
				BatchID:             r.batchID,
				FromState:           fromState,
				ClaimedState:        toState,
				PendingAction:       commonv1.ActionType(commonv1.ActionType_value[r.pendingAction]),
				AttemptCount:        r.attemptCount,
				AmountPaise:         r.amountPaise,
				RootCauseBucket:     bucket,
				Type:                commonv1.RecordType(commonv1.RecordType_value[r.recordType]),
				EVScoreAtDecision:   evScore,
				PRecoveryAtDecision: pRecovery,
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

// recordRescore is scheduleNew's counterpart for the re-entry edges in
// docs/ARCHITECTURE.md section 7: a failed attempt that still has budget
// left moves ClaimedState -> Scoring -> wherever the re-priced economics
// sent it, in one transaction, exactly like a fresh record's New -> Scoring
// -> ... trail. attemptNumber is the attempt that just failed: both the
// updated attempt_count and the audit entries are attributed to it, since
// this is the aftermath of that one attempt, not a new one.
func (s *store) recordRescore(ctx context.Context, c claimedRecord, steps []stateStep, pendingAction commonv1.ActionType, score economics.Score, dueAt *time.Time, attemptNumber int, costPaise int64, now time.Time) error {
	if len(steps) == 0 {
		return fmt.Errorf("rescore %s: no transitions to record", c.RecordID)
	}
	final := steps[len(steps)-1].To
	evScore, pRecovery := scoreColumns(score)

	return pgxpkg.WithTx(ctx, s.pool, func(ctx context.Context, tx pgxpkg.Tx) error {
		if _, err := tx.Exec(ctx, `
			UPDATE record_state SET current_state=$1, attempt_count=$2, pending_action=$3, due_at=$4, ev_score_at_decision=$5, p_recovery_at_decision=$6, last_action_at=$7, updated_at=$7
			WHERE record_id=$8`,
			final.String(), attemptNumber, nullIfUnspecified(pendingAction), dueAt, evScore, pRecovery, now, c.RecordID,
		); err != nil {
			return fmt.Errorf("update record_state for %s: %w", c.RecordID, err)
		}
		// provider_hops is deliberately NOT written in this loop, so it stays
		// NULL. A re-score does not re-classify (scoreAndRoute re-prices what
		// the Classifier already said, with one more attempt spent), so no
		// provider call was made. Carrying the original classification's hops
		// forward onto a later entry would misrepresent the trail as "we asked
		// the model again", which is the quiet overstatement the field exists
		// to prevent (Phase 3 Unit E).
		//
		// The cost of the attempt that just ran belongs on the first step
		// (ClaimedState -> Scoring, the actual aftermath of that attempt);
		// the second step (Scoring -> next state) is a pure decision and
		// costs nothing itself.
		for i, step := range steps {
			cost := int64(0)
			if i == 0 {
				cost = costPaise
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO audit_entry (record_id, batch_id, ts, from_state, to_state, reason, actor, attempt_number, cost_paise)
				VALUES ($1, $2, $3, $4, $5, $6, 'system', $7, $8)`,
				c.RecordID, c.BatchID, now, step.From.String(), step.To.String(), step.Reason, attemptNumber, cost,
			); err != nil {
				return fmt.Errorf("insert rescore audit_entry %s -> %s: %w", step.From, step.To, err)
			}
		}
		return nil
	})
}

// loadNudged reads recordID's current claim-shaped snapshot for
// ReportDelayedOutcome (docs/PHASE5_IMPLEMENTATION.md Unit A). Unlike
// claimDue, nothing is claimed or transitioned here: a NUDGED record is
// already exactly where it should be, resting while it waits for an
// external event (a customer acting on a nudge, hours later) rather than
// a poll. found is false only when recordID has no RECORD_STATE row at
// all; state is always the record's real current state when found, even
// when it is not NUDGED, so a caller can tell "stale/duplicate report for
// a record that already moved on" from "this record_id does not exist".
func (s *store) loadNudged(ctx context.Context, recordID string) (rec claimedRecord, state commonv1.RecordState, found bool, err error) {
	var (
		currentState                 string
		pendingAction                *string
		attemptCount                 int
		amountPaise                  int64
		rootCauseBucket              *string
		evScoreAtDecision, pRecovery *float64
		batchID                      string
		recordType                   string
	)
	err = s.pool.QueryRow(ctx, `
		SELECT rs.current_state, r.batch_id, rs.pending_action, rs.attempt_count, r.amount_paise, rs.root_cause_bucket, rs.ev_score_at_decision, rs.p_recovery_at_decision, r.type
		FROM record_state rs
		JOIN record r ON r.id = rs.record_id
		WHERE rs.record_id = $1`,
		recordID,
	).Scan(&currentState, &batchID, &pendingAction, &attemptCount, &amountPaise, &rootCauseBucket, &evScoreAtDecision, &pRecovery, &recordType)
	if errors.Is(err, pgx.ErrNoRows) {
		return claimedRecord{}, commonv1.RecordState_RECORD_STATE_UNSPECIFIED, false, nil
	}
	if err != nil {
		return claimedRecord{}, commonv1.RecordState_RECORD_STATE_UNSPECIFIED, false, fmt.Errorf("load record_state for %s: %w", recordID, err)
	}

	state = commonv1.RecordState(commonv1.RecordState_value[currentState])
	bucket := commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_UNSPECIFIED
	if rootCauseBucket != nil {
		bucket = commonv1.RootCauseBucket(commonv1.RootCauseBucket_value[*rootCauseBucket])
	}
	var evScore, pRecoveryVal float64
	if evScoreAtDecision != nil {
		evScore = *evScoreAtDecision
	}
	if pRecovery != nil {
		pRecoveryVal = *pRecovery
	}
	// NULL here (nullIfUnspecified's own inverse) is not a schema surprise:
	// it is exactly what every state other than a waiting one legitimately
	// has, which is precisely the case this function's "found but not
	// NUDGED" callers need to handle without erroring.
	action := commonv1.ActionType_ACTION_TYPE_UNSPECIFIED
	if pendingAction != nil {
		action = commonv1.ActionType(commonv1.ActionType_value[*pendingAction])
	}
	rec = claimedRecord{
		RecordID:            recordID,
		BatchID:             batchID,
		FromState:           commonv1.RecordState_RECORD_STATE_NUDGED,
		ClaimedState:        commonv1.RecordState_RECORD_STATE_NUDGED,
		PendingAction:       action,
		AttemptCount:        attemptCount,
		AmountPaise:         amountPaise,
		RootCauseBucket:     bucket,
		Type:                commonv1.RecordType(commonv1.RecordType_value[recordType]),
		EVScoreAtDecision:   evScore,
		PRecoveryAtDecision: pRecoveryVal,
	}
	return rec, state, true, nil
}

// applyResumedOutcome persists a delayed outcome's effect on c.RecordID --
// a single NUDGED -> Recovered step (success) or rescoringPath's two steps
// (a failed attempt re-scored) -- but only if the record is still resting
// in NUDGED at exactly attemptNumber by the time this transaction takes
// the row lock.
//
// The FOR UPDATE here earns its keep in a way claimDue's callers do not
// need repeated for their own writes: claimDue's own claim transition
// already serialises a record so only one execution is ever in flight for
// it (docs/ARCHITECTURE.md section 7a), which is why recordOutcome and
// recordRescore write without locking. ReportDelayedOutcome has no such
// prior claim step -- a NUDGED record just sits there -- and this RPC is
// at-least-once (decisionengine.proto), so two copies of the same report
// can arrive genuinely concurrently. Without this lock both could read
// NUDGED before either writes and double-apply the outcome. With it, the
// second transaction's re-check sees the state the first one already
// moved to and returns applied=false instead.
func (s *store) applyResumedOutcome(ctx context.Context, c claimedRecord, attemptNumber int, steps []stateStep, pendingAction commonv1.ActionType, score economics.Score, dueAt *time.Time, costPaise int64, now time.Time) (bool, error) {
	if len(steps) == 0 {
		return false, fmt.Errorf("apply resumed outcome for %s: no transitions to record", c.RecordID)
	}
	final := steps[len(steps)-1].To
	evScore, pRecovery := scoreColumns(score)

	applied := false
	err := pgxpkg.WithTx(ctx, s.pool, func(ctx context.Context, tx pgxpkg.Tx) error {
		var currentState string
		var currentAttempt int
		if err := tx.QueryRow(ctx, `
			SELECT current_state, attempt_count FROM record_state WHERE record_id = $1 FOR UPDATE`,
			c.RecordID,
		).Scan(&currentState, &currentAttempt); err != nil {
			return fmt.Errorf("lock record_state for %s: %w", c.RecordID, err)
		}
		if currentState != commonv1.RecordState_RECORD_STATE_NUDGED.String() || currentAttempt != attemptNumber {
			// Discarded: the record moved on between the caller's read and
			// this lock, most often a duplicate delayed-outcome report
			// arriving after the first copy already applied. Not an error.
			return nil
		}

		if _, err := tx.Exec(ctx, `
			UPDATE record_state SET current_state=$1, attempt_count=$2, pending_action=$3, due_at=$4, ev_score_at_decision=$5, p_recovery_at_decision=$6, last_action_at=$7, updated_at=$7
			WHERE record_id=$8`,
			final.String(), attemptNumber, nullIfUnspecified(pendingAction), dueAt, evScore, pRecovery, now, c.RecordID,
		); err != nil {
			return fmt.Errorf("update record_state for %s: %w", c.RecordID, err)
		}
		for i, step := range steps {
			cost := int64(0)
			if i == 0 {
				cost = costPaise
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO audit_entry (record_id, batch_id, ts, from_state, to_state, reason, actor, attempt_number, cost_paise)
				VALUES ($1, $2, $3, $4, $5, $6, 'system', $7, $8)`,
				c.RecordID, c.BatchID, now, step.From.String(), step.To.String(), step.Reason, attemptNumber, cost,
			); err != nil {
				return fmt.Errorf("insert resumed-outcome audit_entry %s -> %s: %w", step.From, step.To, err)
			}
		}
		applied = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return applied, nil
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

// scoreColumns returns the values to store for score's EV and recovery
// probability, or (nil, nil) when score is the zero value: a record that
// never reached the economics gate (an explicit escalation) has no winning
// score to snapshot, and storing 0 would misrepresent "priced at zero" as
// "never priced" (docs/PHASE2_IMPLEMENTATION.md Unit G). A real score always
// has a non-UNSPECIFIED Action, since it is the winning candidate returned
// by economics.Model.Best.
func scoreColumns(score economics.Score) (evScore, pRecovery any) {
	if score.Action == commonv1.ActionType_ACTION_TYPE_UNSPECIFIED {
		return nil, nil
	}
	return score.EVPaise, score.PRecovery
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

// instrumentHistoryLimit caps loadInstrumentHistory: a popular instrument
// accumulates rows indefinitely, and an unbounded read here is both a
// growing per-record query and a growing prompt (Phase 3 Unit F).
const instrumentHistoryLimit = 10

// loadAttemptRows returns recordID's own prior intervention attempts, oldest
// first, for ClassifyRequest.history (classifier.proto: "so the model can
// reason about what has already been tried rather than starting cold").
//
// Deliberately not loadAttemptHistory reused: that query returns aggregate
// counts and a cooldown timestamp for the guardrails, this returns rows for
// the classifier's prompt, and merging them would couple a compliance check
// to a prompt-shaping read (Phase 3 Unit F).
func (s *store) loadAttemptRows(ctx context.Context, recordID string) ([]*commonv1.InterventionAttempt, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT attempt_number, action_type, outcome, executed_at, cost_paise
		FROM intervention_attempt
		WHERE record_id = $1
		ORDER BY executed_at ASC`, recordID)
	if err != nil {
		return nil, fmt.Errorf("load attempt rows for %s: %w", recordID, err)
	}
	defer rows.Close()

	attempts, err := scanAttemptRows(rows)
	if err != nil {
		return nil, fmt.Errorf("load attempt rows for %s: %w", recordID, err)
	}
	return attempts, nil
}

// loadInstrumentHistory returns attempts on OTHER records sharing
// instrumentRef, most recent first, capped at instrumentHistoryLimit, for
// ClassifyRequest.instrument_history: "signal for distinguishing this rail
// is flaky right now from this card is dead" (classifier.proto). Callers
// must not call this with an empty instrumentRef (nullable in the schema);
// that means "skip the query", not "query for the empty string".
//
// record_instrument_idx (00001_initial_schema.sql) is partial on
// instrument_ref IS NOT NULL, so this join is indexed without a migration.
func (s *store) loadInstrumentHistory(ctx context.Context, instrumentRef, excludeRecordID string) ([]*commonv1.InterventionAttempt, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT ia.attempt_number, ia.action_type, ia.outcome, ia.executed_at, ia.cost_paise
		FROM intervention_attempt ia
		JOIN record r ON r.id = ia.record_id
		WHERE r.instrument_ref = $1 AND ia.record_id != $2
		ORDER BY ia.executed_at DESC
		LIMIT $3`, instrumentRef, excludeRecordID, instrumentHistoryLimit)
	if err != nil {
		return nil, fmt.Errorf("load instrument history for %s: %w", instrumentRef, err)
	}
	defer rows.Close()

	attempts, err := scanAttemptRows(rows)
	if err != nil {
		return nil, fmt.Errorf("load instrument history for %s: %w", instrumentRef, err)
	}
	return attempts, nil
}

// scanAttemptRows scans the common projection shared by loadAttemptRows and
// loadInstrumentHistory: attempt_number, action_type, outcome, executed_at,
// cost_paise, in that order.
func scanAttemptRows(rows pgx.Rows) ([]*commonv1.InterventionAttempt, error) {
	var attempts []*commonv1.InterventionAttempt
	for rows.Next() {
		var (
			attemptNumber int32
			actionType    string
			outcome       string
			executedAt    time.Time
			costPaise     int64
		)
		if err := rows.Scan(&attemptNumber, &actionType, &outcome, &executedAt, &costPaise); err != nil {
			return nil, fmt.Errorf("scan attempt row: %w", err)
		}
		attempts = append(attempts, &commonv1.InterventionAttempt{
			AttemptNumber: attemptNumber,
			ActionType:    commonv1.ActionType(commonv1.ActionType_value[actionType]),
			Outcome:       commonv1.Outcome(commonv1.Outcome_value[outcome]),
			ExecutedAt:    timestamppb.New(executedAt),
			CostPaise:     costPaise,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return attempts, nil
}

// encodedHops renders a classification's provider hops for storage, returning
// NULL rather than an error when they cannot be encoded.
//
// The asymmetry is deliberate. The hops are diagnostic; the classification and
// the state transition are not. Failing the transaction because a hop label
// contained a delimiter would lose a real record over a cosmetic problem, and
// the record is what matters. hopcodec.Encode only rejects input that
// provider.NewChain already refuses at startup, so this should be unreachable
// in practice, which is exactly why it warrants a Warn rather than silence:
// reaching it means an invariant somewhere upstream has gone.
//
// Returns a *string so a nil result stores SQL NULL. NULL means "no
// classification happened here", which is different from the empty string and
// the column's comment (migration 00005) says so.
func encodedHops(hops []*commonv1.ProviderHop, log *slog.Logger) *string {
	if len(hops) == 0 {
		return nil
	}
	encoded, err := hopcodec.Encode(hops)
	if err != nil {
		// log is the record-scoped logger from HandleMessage, so this
		// already carries record_id and batch_id.
		log.Warn("could not encode provider hops, storing NULL", logger.KeyError, err)
		return nil
	}
	return &encoded
}
