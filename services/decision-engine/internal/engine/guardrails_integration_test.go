//go:build integration

// loadAttemptHistory derives the guardrails' counters straight from
// INTERVENTION_ATTEMPT rather than storing them, so the SQL is the place this
// can silently be wrong. These tests run it against real Postgres, because the
// join semantics ARE the behaviour (docs/ENGINEERING.md section 1, do not mock
// what you own).
package engine

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	pgxpkg "github.com/thisizaro/Momotaro/internal/platform/pgx"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

// seedAttempt inserts one executed intervention against recordID. Its cleanup
// registers after seedRecord's, and t.Cleanup runs last-in-first-out, so these
// rows are removed before the record they reference and the foreign key holds.
func seedAttempt(ctx context.Context, t *testing.T, pool *pgxpkg.Pool, recordID string, n int, action commonv1.ActionType, executedAt time.Time) {
	t.Helper()
	id := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		INSERT INTO intervention_attempt (id, record_id, attempt_number, action_type, outcome, executed_at, cost_paise)
		VALUES ($1, $2, $3, $4, $5, $6, 0)`,
		id, recordID, n, action.String(), commonv1.Outcome_OUTCOME_FAILURE.String(), executedAt,
	); err != nil {
		t.Fatalf("seed intervention_attempt: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM intervention_attempt WHERE id = $1`, id)
	})
}

// A record nobody has spent anything on must come back all zeroes. This is the
// specific case a LEFT JOIN gets wrong: COUNT(*) scores the single all-NULL
// joined row as 1, so a fresh record would look like it had already used a
// retry and the very first attempt could be refused.
func TestLoadAttemptHistoryCountsNothingForARecordWithNoAttempts(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	_, recordID := seedRecord(ctx, t, pool)

	h, err := newStore(pool).loadAttemptHistory(ctx, recordID)
	if err != nil {
		t.Fatalf("loadAttemptHistory: %v", err)
	}

	if h.Retries != 0 || h.Contacts != 0 {
		t.Errorf("fresh record: retries=%d contacts=%d, want 0 and 0", h.Retries, h.Contacts)
	}
	if h.LastContactAt != nil {
		t.Errorf("fresh record: LastContactAt = %v, want nil, nobody has been contacted", *h.LastContactAt)
	}
	if h.RecordCreatedAt.IsZero() {
		t.Error("RecordCreatedAt is zero, the recovery window would be computed against the epoch")
	}
}

// The two budgets are independent (PRD.md section 11 caps retries and contacts
// separately), so a query that lumped them together would let nudges eat the
// retry budget.
func TestLoadAttemptHistoryCountsRetriesAndContactsSeparately(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	_, recordID := seedRecord(ctx, t, pool)
	now := time.Now().UTC().Truncate(time.Millisecond)

	seedAttempt(ctx, t, pool, recordID, 1, commonv1.ActionType_ACTION_TYPE_RETRY, now.Add(-3*time.Hour))
	seedAttempt(ctx, t, pool, recordID, 2, commonv1.ActionType_ACTION_TYPE_RETRY, now.Add(-2*time.Hour))
	seedAttempt(ctx, t, pool, recordID, 3, commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER, now.Add(-time.Hour))

	h, err := newStore(pool).loadAttemptHistory(ctx, recordID)
	if err != nil {
		t.Fatalf("loadAttemptHistory: %v", err)
	}
	if h.Retries != 2 {
		t.Errorf("Retries = %d, want 2", h.Retries)
	}
	if h.Contacts != 1 {
		t.Errorf("Contacts = %d, want 1", h.Contacts)
	}
}

// Both nudge subtypes are contacts. Counting only one would let a record be
// messaged twice as often as the cap allows.
func TestLoadAttemptHistoryCountsBothNudgeSubtypesAsContacts(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	_, recordID := seedRecord(ctx, t, pool)
	now := time.Now().UTC()

	seedAttempt(ctx, t, pool, recordID, 1, commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER, now.Add(-2*time.Hour))
	seedAttempt(ctx, t, pool, recordID, 2, commonv1.ActionType_ACTION_TYPE_NUDGE_METHOD_UPDATE, now.Add(-time.Hour))

	h, err := newStore(pool).loadAttemptHistory(ctx, recordID)
	if err != nil {
		t.Fatalf("loadAttemptHistory: %v", err)
	}
	if h.Contacts != 2 {
		t.Errorf("Contacts = %d, want 2: both nudge subtypes are customer contacts", h.Contacts)
	}
}

// The cooldown is measured from the most recent contact, so MAX and not MIN.
// Getting this backwards would compute the cooldown from the oldest message
// and release the next one far too early.
func TestLoadAttemptHistoryReportsTheMostRecentContact(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	_, recordID := seedRecord(ctx, t, pool)
	now := time.Now().UTC().Truncate(time.Millisecond)
	older, newer := now.Add(-5*time.Hour), now.Add(-1*time.Hour)

	seedAttempt(ctx, t, pool, recordID, 1, commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER, older)
	seedAttempt(ctx, t, pool, recordID, 2, commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER, newer)

	h, err := newStore(pool).loadAttemptHistory(ctx, recordID)
	if err != nil {
		t.Fatalf("loadAttemptHistory: %v", err)
	}
	if h.LastContactAt == nil {
		t.Fatal("LastContactAt is nil after two contacts")
	}
	if got := h.LastContactAt.UTC(); !got.Equal(newer) {
		t.Errorf("LastContactAt = %v, want the most recent contact %v", got, newer)
	}
}

// A retry is not a contact, so it must not restart the contact cooldown.
func TestLoadAttemptHistoryIgnoresRetriesWhenDatingTheLastContact(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	_, recordID := seedRecord(ctx, t, pool)
	now := time.Now().UTC()

	seedAttempt(ctx, t, pool, recordID, 1, commonv1.ActionType_ACTION_TYPE_RETRY, now.Add(-time.Minute))

	h, err := newStore(pool).loadAttemptHistory(ctx, recordID)
	if err != nil {
		t.Fatalf("loadAttemptHistory: %v", err)
	}
	if h.LastContactAt != nil {
		t.Errorf("LastContactAt = %v after a retry only, want nil: a retry is not a customer contact", *h.LastContactAt)
	}
}

// Budgets are per record. A query missing its WHERE would let one noisy record
// exhaust every other record's budget.
func TestLoadAttemptHistoryIgnoresOtherRecordsAttempts(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	_, mine := seedRecord(ctx, t, pool)
	_, theirs := seedRecord(ctx, t, pool)
	now := time.Now().UTC()

	seedAttempt(ctx, t, pool, theirs, 1, commonv1.ActionType_ACTION_TYPE_RETRY, now)
	seedAttempt(ctx, t, pool, theirs, 2, commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER, now)

	h, err := newStore(pool).loadAttemptHistory(ctx, mine)
	if err != nil {
		t.Fatalf("loadAttemptHistory: %v", err)
	}
	if h.Retries != 0 || h.Contacts != 0 {
		t.Errorf("another record's attempts leaked in: retries=%d contacts=%d, want 0 and 0", h.Retries, h.Contacts)
	}
}

// An unknown record must be an error, never a zero-valued history. Returning
// zeroes would silently grant a full budget to a record that does not exist.
func TestLoadAttemptHistoryErrorsForAnUnknownRecord(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)

	if _, err := newStore(pool).loadAttemptHistory(ctx, uuid.NewString()); err == nil {
		t.Error("loadAttemptHistory for an unknown record returned no error, want one")
	}
}
