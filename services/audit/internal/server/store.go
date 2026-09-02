package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/thisizaro/Momotaro/internal/platform/hopcodec"
	pgxpkg "github.com/thisizaro/Momotaro/internal/platform/pgx"
	auditv1 "github.com/thisizaro/Momotaro/proto/gen/audit/v1"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// store is Audit's only access point to Postgres. Audit reads RECORD,
// RECORD_STATE and AUDIT_ENTRY but writes none of them (docs/ARCHITECTURE.md
// section 10a: the owning service writes its own state transactionally).
// Keeping every SQL statement behind this type is what lets server.go read
// as orchestration rather than a mix of gRPC handling and SQL
// (docs/ENGINEERING.md section 14).
type store struct {
	pool *pgxpkg.Pool
}

func newStore(pool *pgxpkg.Pool) *store {
	return &store{pool: pool}
}

// loadRecord returns the RECORD row for recordID, or a NotFound-shaped
// error via errors.Is(err, pgxpkg.ErrNoRows) for the caller to translate.
func (s *store) loadRecord(ctx context.Context, recordID string) (*commonv1.Record, error) {
	var rec commonv1.Record
	var typeStr string
	var instrumentRef sql.NullString
	var createdAt time.Time

	err := s.pool.QueryRow(ctx, `
		SELECT id, batch_id, type, amount_paise, currency, failure_code, instrument_ref, created_at
		FROM record WHERE id = $1`, recordID,
	).Scan(&rec.Id, &rec.BatchId, &typeStr, &rec.AmountPaise, &rec.Currency, &rec.FailureCode, &instrumentRef, &createdAt)
	if err != nil {
		return nil, err
	}

	rec.Type = commonv1.RecordType(commonv1.RecordType_value[typeStr])
	rec.CreatedAt = timestamppb.New(createdAt)
	if instrumentRef.Valid {
		rec.InstrumentRef = instrumentRef.String
	}
	return &rec, nil
}

// loadCurrentState returns RECORD_STATE_UNSPECIFIED, nil for a record that
// has been ingested but not yet touched by Decision Engine: that is normal,
// not an error.
func (s *store) loadCurrentState(ctx context.Context, recordID string) (commonv1.RecordState, error) {
	var stateStr string
	err := s.pool.QueryRow(ctx, `SELECT current_state FROM record_state WHERE record_id = $1`, recordID).Scan(&stateStr)
	if err != nil {
		if errors.Is(err, pgxpkg.ErrNoRows) {
			return commonv1.RecordState_RECORD_STATE_UNSPECIFIED, nil
		}
		return commonv1.RecordState_RECORD_STATE_UNSPECIFIED, fmt.Errorf("query record_state for %s: %w", recordID, err)
	}
	return commonv1.RecordState(commonv1.RecordState_value[stateStr]), nil
}

// loadAuditEntries returns a record's full trail, oldest first.
func (s *store) loadAuditEntries(ctx context.Context, recordID string) ([]*auditv1.AuditEntry, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT ts, from_state, to_state, reason, rationale, source, actor, attempt_number, cost_paise, message_text, provider_hops, decision_trace
		FROM audit_entry WHERE record_id = $1 ORDER BY ts ASC, id ASC`, recordID)
	if err != nil {
		return nil, fmt.Errorf("query audit_entry for %s: %w", recordID, err)
	}
	defer rows.Close()

	var entries []*auditv1.AuditEntry
	for rows.Next() {
		var ts time.Time
		var fromState, toState, reason string
		var rationale, source, actor, messageText, providerHops, decisionTrace sql.NullString
		var attemptNumber sql.NullInt32
		var costPaise sql.NullInt64

		if err := rows.Scan(&ts, &fromState, &toState, &reason, &rationale, &source, &actor, &attemptNumber, &costPaise, &messageText, &providerHops, &decisionTrace); err != nil {
			return nil, fmt.Errorf("scan audit_entry for %s: %w", recordID, err)
		}

		// Worth failing on rather than skipping: the hops exist to show what
		// was tried, so an entry silently returned without them is
		// indistinguishable from a classification that tried nothing
		// (Phase 3 Unit E).
		hops, err := hopcodec.Decode(providerHops.String)
		if err != nil {
			return nil, fmt.Errorf("decode provider_hops for %s: %w", recordID, err)
		}

		// decision_trace is nullable: most entries never scored a decision
		// (decodeDecisionTrace treats an empty string, what sql.NullString
		// yields for SQL NULL, as "no trace" and returns nil rather than an
		// empty message), so scanning into a plain string would collapse
		// "never scored" and "scored and empty" into the same value.
		trace, err := decodeDecisionTrace(decisionTrace.String)
		if err != nil {
			return nil, fmt.Errorf("decode decision_trace for %s: %w", recordID, err)
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
			Hops:          hops,
			DecisionTrace: trace,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate audit_entry for %s: %w", recordID, err)
	}
	return entries, nil
}

// recordSnapshot is one record's worth of input to verifyInvariants
// (verify.go): whether Decision Engine has touched it yet, its current
// state if so, and its full ordered transition history. scanRecords is the
// only place this shape is built, so verify.go never touches SQL.
type recordSnapshot struct {
	RecordID     string
	HasState     bool
	CurrentState commonv1.RecordState
	Entries      []transition // oldest first
}

// scanRecords returns a snapshot of every record in scope for
// VerifyInvariants: every record if batchID is "", otherwise only that
// batch's records. Two queries rather than a join, because a record with no
// RECORD_STATE row yet (ingested, not yet processed) still needs to be
// counted, and a LEFT JOIN against AUDIT_ENTRY as well would multiply rows
// per entry, complicating the "no state yet" check for no benefit here.
func (s *store) scanRecords(ctx context.Context, batchID string) ([]recordSnapshot, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT r.id, rs.current_state
		FROM record r
		LEFT JOIN record_state rs ON rs.record_id = r.id
		WHERE $1 = '' OR r.batch_id::text = $1`, batchID)
	if err != nil {
		return nil, fmt.Errorf("scan records for batch %q: %w", batchID, err)
	}
	defer rows.Close()

	snapshots := make(map[string]*recordSnapshot)
	var order []string
	for rows.Next() {
		var id string
		var stateStr sql.NullString
		if err := rows.Scan(&id, &stateStr); err != nil {
			return nil, fmt.Errorf("scan record row: %w", err)
		}
		snap := &recordSnapshot{RecordID: id}
		if stateStr.Valid {
			snap.HasState = true
			snap.CurrentState = commonv1.RecordState(commonv1.RecordState_value[stateStr.String])
		}
		snapshots[id] = snap
		order = append(order, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate records: %w", err)
	}

	entryRows, err := s.pool.Query(ctx, `
		SELECT ae.record_id, ae.from_state, ae.to_state
		FROM audit_entry ae
		JOIN record r ON r.id = ae.record_id
		WHERE $1 = '' OR r.batch_id::text = $1
		ORDER BY ae.record_id, ae.ts, ae.id`, batchID)
	if err != nil {
		return nil, fmt.Errorf("scan audit entries for batch %q: %w", batchID, err)
	}
	defer entryRows.Close()

	for entryRows.Next() {
		var recordID, fromStr, toStr string
		if err := entryRows.Scan(&recordID, &fromStr, &toStr); err != nil {
			return nil, fmt.Errorf("scan audit_entry row: %w", err)
		}
		snap, ok := snapshots[recordID]
		if !ok {
			// Cannot happen: audit_entry.record_id is a foreign key into
			// record, and the first query has no filter narrower than
			// this one's JOIN. Guard anyway rather than a nil dereference.
			continue
		}
		snap.Entries = append(snap.Entries, transition{
			From: commonv1.RecordState(commonv1.RecordState_value[fromStr]),
			To:   commonv1.RecordState(commonv1.RecordState_value[toStr]),
		})
	}
	if err := entryRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate audit entries: %w", err)
	}

	result := make([]recordSnapshot, 0, len(order))
	for _, id := range order {
		result = append(result, *snapshots[id])
	}
	return result, nil
}
