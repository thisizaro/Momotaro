//go:build e2e

// Unit H: Batch correctness invariants (docs/PHASE2_IMPLEMENTATION.md).
//
// Asserts the two headline correctness claims from docs/PRD.md sections 9
// and 10 against a live batch: zero stopping-rule violations (no record's
// real retry/contact history ever exceeds its guardrail cap, and no two
// contacts land closer together than the cooldown), and 100% audit trail
// completeness (Audit.VerifyInvariants reports zero violations).
//
// Why this needs seeded history, not a pure organic run: ports/stub.go's
// StubRecovery is a deterministic script -- "RETRY, attempt 1 -> SUCCESS,
// attempt 2+ -> FAILURE" -- and every fresh record's first retry attempt is
// attempt 1. That means, submitted through the real HTTP API with no other
// help, a record always recovers on its first attempt and never naturally
// reaches a second one, so MAX_RETRIES=3 and MAX_CONTACTS=3 are
// structurally unreachable through an organic run. Written before this
// finding, a test that only submitted a batch and waited would pass because
// nothing ever approached a cap -- the exact failure mode this unit's own
// LLD warns about.
//
// The fix used here is the same one already used and accepted for Units G
// and L this session: seed a record's prior INTERVENTION_ATTEMPT rows
// directly in Postgres (the exact table loadAttemptHistory,
// decision-engine/internal/engine/store.go, reads from -- confirmed by
// reading it before writing this) so a record enters the scheduler's next
// live claim already sitting near its cap, then let the real, live
// Decision Engine, Executor and Audit process everything after that. Only
// the unreachable ramp-up is fabricated; every guardrail decision, every
// state transition and every audit write from that point on is real
// production code running against a real database.
//
// The guardrail RULES themselves (retryBudgetSpent, contactBlocked, the
// cooldown release) are already exhaustively unit-tested in isolation in
// services/decision-engine/internal/engine/guardrails_test.go. This test's
// job is to prove the WIRING: that real seeded history reaches the real
// guardrails through the real claim/execute/re-score cycle and the real
// numbers never cross the line, not to re-derive the rules from scratch.
package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	pgxpkg "github.com/thisizaro/Momotaro/internal/platform/pgx"
	auditv1 "github.com/thisizaro/Momotaro/proto/gen/audit/v1"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

// Defaults from services/decision-engine/cmd/main.go: MAX_RETRIES=3,
// MAX_CONTACTS=3. Neither is overridden by the harness, so these must match
// what the live service actually enforces.
const (
	wantMaxRetries  = 3
	wantMaxContacts = 3
)

func TestBatchCorrectnessInvariants(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// commonEnv sets DEMO_TIME_SCALE=300000 (walking_skeleton_test.go), and
	// TRANSIENT_BANK's retry timing (schedule.go's retryDueAt) scales
	// RETRY_DELAY by that same factor, so a plain "10s" here would collapse
	// to microseconds. "3000000s" scales down to a real ~10s window --
	// long enough to reliably observe RETRY_SCHEDULED and seed history
	// before the scheduler claims it (poll interval is 300ms), short
	// enough to stay well under pipelineWait.
	stack := startStack(ctx, t, "3000000s")
	pool := connectPool(ctx, t)
	auditClient := dialAudit(ctx, t, stack.auditAddr)

	source := "e2e-batch-invariants-" + uuid.NewString()
	instrRetries := "card_h_retries_" + uuid.NewString()
	instrContacts := "card_h_contacts_" + uuid.NewString()
	instrHappy := "card_h_happy_" + uuid.NewString()

	// Amount large enough to stay economic across repeated attempts against
	// configs/intervention_costs.yaml and configs/recovery_priors.yaml.
	const amountPaise = 900000

	body := fmt.Sprintf(`{
		"source":%q,
		"records":[
			{"type":"PAYMENT","amount_paise":%d,"currency":"INR","failure_code":"BANK_TIMEOUT","instrument_ref":%q},
			{"type":"PAYMENT","amount_paise":%d,"currency":"INR","failure_code":"BANK_TIMEOUT","instrument_ref":%q},
			{"type":"PAYMENT","amount_paise":%d,"currency":"INR","failure_code":"BANK_TIMEOUT","instrument_ref":%q}
		]
	}`, source, amountPaise, instrRetries, amountPaise, instrContacts, amountPaise, instrHappy)

	resp := submitBatch(ctx, t, stack.gatewayHTTP, body)
	if resp.AcceptedCount != 3 {
		t.Fatalf("accepted_count = %d, want 3", resp.AcceptedCount)
	}
	batchID := resp.BatchID

	recRetries := recordIDByInstrument(ctx, t, pool, batchID, instrRetries)
	recContacts := recordIDByInstrument(ctx, t, pool, batchID, instrContacts)
	recHappy := recordIDByInstrument(ctx, t, pool, batchID, instrHappy)

	// All three classify as TRANSIENT_BANK (BANK_TIMEOUT), and Executor now
	// calls the real World Simulator for RETRY/NUDGE (Phase 5 Units C/D),
	// which requires a GROUND_TRUTH row per record. recHappy needs to
	// recover organically on its first attempt: recovery_probability=1.0.
	// recRetries and recContacts each get exactly ONE real live call before
	// this test's own fabricated history takes over (the comments below
	// already establish that), and that one call must fail to trip
	// MAX_RETRIES the way the old deterministic stub's "attempt 2+ fails"
	// script did: recovery_probability=0.0. wrong_action_probability=0.0
	// too, since recContacts' re-score may pick a real NUDGE_REMINDER
	// (not TRANSIENT_BANK's correct action, so it rolls against
	// wrong_action_probability, not recovery_probability) and that must
	// also resolve immediately and deterministically rather than park in
	// PENDING inside waitUntilResting's 45s budget.
	for rid, recoveryP := range map[string]float64{recHappy: 1.0, recRetries: 0.0, recContacts: 0.0} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO ground_truth (record_id, true_bucket, recovery_probability, wrong_action_probability, response_delay_seconds)
			VALUES ($1, 'ROOT_CAUSE_BUCKET_TRANSIENT_BANK', $2, 0.0, 0)`, rid, recoveryP); err != nil {
			t.Fatalf("seed ground_truth for %s: %v", rid, err)
		}
	}

	t.Cleanup(func() {
		bg := context.Background()
		for _, rid := range []string{recRetries, recContacts, recHappy} {
			_, _ = pool.Exec(bg, `DELETE FROM ground_truth WHERE record_id = $1`, rid)
			_, _ = pool.Exec(bg, `DELETE FROM audit_entry WHERE record_id = $1`, rid)
			_, _ = pool.Exec(bg, `DELETE FROM intervention_attempt WHERE record_id = $1`, rid)
			_, _ = pool.Exec(bg, `DELETE FROM record_state WHERE record_id = $1`, rid)
			_, _ = pool.Exec(bg, `DELETE FROM record WHERE id = $1`, rid)
		}
		_, _ = pool.Exec(bg, `DELETE FROM batch WHERE id = $1`, batchID)
	})

	// --- Wait for the two seeded records to reach their first real
	// scheduling decision, then plant fabricated history before the
	// scheduler claims them. ---
	waitForExactRecordState(ctx, t, pool, recRetries,
		commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED,
		"record should classify to TRANSIENT_BANK and schedule a retry")
	waitForExactRecordState(ctx, t, pool, recContacts,
		commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED,
		"record should classify to TRANSIENT_BANK and schedule a retry")

	// recRetries: two prior failed retries already used (MaxRetries-1). The
	// live claim that follows executes as attempt 3, which StubRecovery
	// fails (attemptNumber >= 2), pushing real Retries to exactly
	// MaxRetries and tripping the real guardrail.
	seedAttemptHistory(ctx, t, pool, recRetries, []seededAttempt{
		{n: 1, action: commonv1.ActionType_ACTION_TYPE_RETRY, outcome: commonv1.Outcome_OUTCOME_FAILURE, at: time.Now().Add(-2 * time.Minute)},
		{n: 2, action: commonv1.ActionType_ACTION_TYPE_RETRY, outcome: commonv1.Outcome_OUTCOME_FAILURE, at: time.Now().Add(-1 * time.Minute)},
	})

	// recContacts: the live claim that follows executes whatever action the
	// record's real (unseeded) first classify already decided on --
	// TRANSIENT_BANK always recommends RETRY -- regardless of seeded
	// history, since the scheduler runs the already-chosen pending_action
	// and only the guardrails re-check AFTER that attempt fails. So this
	// seeds two prior retries (same as recRetries: MaxRetries-1) plus two
	// prior contacts safely outside CONTACT_COOLDOWN (default 24h). The one
	// live claim executes as attempt 3 (RETRY, forced FAILURE by the stub),
	// pushing real Retries to exactly MaxRetries -- not past it -- and the
	// re-score that follows finds RETRY blocked but NUDGE still permitted
	// (Contacts=2 < 3, cooldown clear), so whichever action the real
	// economics scorer picks next respects both caps for real.
	seedAttemptHistory(ctx, t, pool, recContacts, []seededAttempt{
		{n: 1, action: commonv1.ActionType_ACTION_TYPE_RETRY, outcome: commonv1.Outcome_OUTCOME_FAILURE, at: time.Now().Add(-2 * time.Minute)},
		{n: 2, action: commonv1.ActionType_ACTION_TYPE_RETRY, outcome: commonv1.Outcome_OUTCOME_FAILURE, at: time.Now().Add(-1 * time.Minute)},
		{n: 3, action: commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER, outcome: commonv1.Outcome_OUTCOME_PENDING, at: time.Now().Add(-60 * time.Hour)},
		{n: 4, action: commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER, outcome: commonv1.Outcome_OUTCOME_PENDING, at: time.Now().Add(-30 * time.Hour)},
	})

	// --- Let the real system finish processing all three records. ---
	waitUntilResting(ctx, t, pool, recRetries)
	waitUntilResting(ctx, t, pool, recContacts)
	waitForExactRecordState(ctx, t, pool, recHappy,
		commonv1.RecordState_RECORD_STATE_RECOVERED,
		"unseeded record should recover organically on its first attempt")

	// --- Assert: no record's real history ever crossed a cap. ---
	for _, rid := range []string{recRetries, recContacts, recHappy} {
		retries, contacts := countByAction(ctx, t, pool, rid)
		if retries > wantMaxRetries {
			t.Errorf("record %s: real RETRY attempts = %d, want <= %d (MAX_RETRIES exceeded)", rid, retries, wantMaxRetries)
		}
		if contacts > wantMaxContacts {
			t.Errorf("record %s: real contact attempts = %d, want <= %d (MAX_CONTACTS exceeded)", rid, contacts, wantMaxContacts)
		}
	}
	t.Logf("recRetries=%s recContacts=%s recHappy=%s", recRetries, recContacts, recHappy)

	// --- Assert: no two contacts on recContacts landed closer together
	// than CONTACT_COOLDOWN (default 24h), across BOTH the seeded and any
	// real contact the live system added. ---
	assertContactsRespectCooldown(ctx, t, pool, recContacts, 24*time.Hour)

	// --- Assert: Audit.VerifyInvariants reports zero violations for the
	// whole batch: every trail complete, no impossible transition. ---
	viCtx, viCancel := context.WithTimeout(ctx, 10*time.Second)
	defer viCancel()
	vi, err := auditClient.VerifyInvariants(viCtx, &auditv1.VerifyInvariantsRequest{BatchId: batchID})
	if err != nil {
		t.Fatalf("VerifyInvariants: %v", err)
	}
	if vi.GetRecordsChecked() != 3 {
		t.Errorf("VerifyInvariants RecordsChecked = %d, want 3", vi.GetRecordsChecked())
	}
	if vi.GetIncompleteAuditTrails() != 0 || vi.GetImpossibleTransitions() != 0 || vi.GetStoppingRuleViolations() != 0 {
		t.Errorf("VerifyInvariants reported violations: %+v", vi)
	}
}

// --- helpers ----------------------------------------------------------------

func recordIDByInstrument(ctx context.Context, t *testing.T, p *pgxpkg.Pool, batchID, instrumentRef string) string {
	t.Helper()
	var id string
	if err := p.QueryRow(ctx,
		`SELECT id FROM record WHERE batch_id = $1 AND instrument_ref = $2`, batchID, instrumentRef,
	).Scan(&id); err != nil {
		t.Fatalf("find record for instrument_ref %s: %v", instrumentRef, err)
	}
	return id
}

type seededAttempt struct {
	n       int
	action  commonv1.ActionType
	outcome commonv1.Outcome
	at      time.Time
}

// seedAttemptHistory plants a record's prior INTERVENTION_ATTEMPT rows and
// advances record_state.attempt_count to match, so the next live claim
// executes as attempt len(attempts)+1 and the guardrails' real history
// query (loadAttemptHistory, store.go) sees this fabricated past on the
// very next real scoring decision. attempt_number is a single sequential
// counter per record shared across action types (the DB's UNIQUE
// (record_id, attempt_number) constraint has no action_type column), so
// callers must number attempts 1..N in the order they claim they happened.
func seedAttemptHistory(ctx context.Context, t *testing.T, p *pgxpkg.Pool, recordID string, attempts []seededAttempt) {
	t.Helper()
	for _, a := range attempts {
		if _, err := p.Exec(ctx, `
			INSERT INTO intervention_attempt (id, record_id, attempt_number, action_type, outcome, executed_at, cost_paise)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			uuid.NewString(), recordID, a.n, a.action.String(), a.outcome.String(), a.at, 500,
		); err != nil {
			t.Fatalf("seed intervention_attempt %d for %s: %v", a.n, recordID, err)
		}
	}
	if _, err := p.Exec(ctx,
		`UPDATE record_state SET attempt_count = $2 WHERE record_id = $1`, recordID, len(attempts),
	); err != nil {
		t.Fatalf("advance attempt_count for %s: %v", recordID, err)
	}
}

// countByAction returns the record's real RETRY and NUDGE* attempt counts.
func countByAction(ctx context.Context, t *testing.T, p *pgxpkg.Pool, recordID string) (retries, contacts int) {
	t.Helper()
	err := p.QueryRow(ctx, `
		SELECT COUNT(*) FILTER (WHERE action_type = $2),
		       COUNT(*) FILTER (WHERE action_type = ANY($3))
		FROM intervention_attempt WHERE record_id = $1`,
		recordID,
		commonv1.ActionType_ACTION_TYPE_RETRY.String(),
		[]string{
			commonv1.ActionType_ACTION_TYPE_NUDGE_METHOD_UPDATE.String(),
			commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER.String(),
		},
	).Scan(&retries, &contacts)
	if err != nil {
		t.Fatalf("count attempts for %s: %v", recordID, err)
	}
	return retries, contacts
}

// assertContactsRespectCooldown fails if any two contact timestamps for
// recordID are closer together than cooldown.
func assertContactsRespectCooldown(ctx context.Context, t *testing.T, p *pgxpkg.Pool, recordID string, cooldown time.Duration) {
	t.Helper()
	rows, err := p.Query(ctx, `
		SELECT executed_at FROM intervention_attempt
		WHERE record_id = $1 AND action_type = ANY($2)
		ORDER BY executed_at`,
		recordID,
		[]string{
			commonv1.ActionType_ACTION_TYPE_NUDGE_METHOD_UPDATE.String(),
			commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER.String(),
		},
	)
	if err != nil {
		t.Fatalf("query contact timestamps for %s: %v", recordID, err)
	}
	defer rows.Close()

	var times []time.Time
	for rows.Next() {
		var ts time.Time
		if err := rows.Scan(&ts); err != nil {
			t.Fatalf("scan contact timestamp for %s: %v", recordID, err)
		}
		times = append(times, ts)
	}
	for i := 1; i < len(times); i++ {
		gap := times[i].Sub(times[i-1])
		if gap < cooldown {
			t.Errorf("record %s: contacts at %s and %s are %s apart, want >= %s (CONTACT_COOLDOWN)",
				recordID, times[i-1], times[i], gap, cooldown)
		}
	}
}

// waitUntilResting polls until recordID's current_state stops changing
// across two consecutive checks (a rough proxy for "the pipeline is done
// with this record for now"), or the deadline passes. Used instead of
// waiting for one specific terminal state because these two records'
// exact resting state depends on which permitted action the live
// economics scorer picks once the seeded caps take effect, which this
// test deliberately does not over-specify.
func waitUntilResting(ctx context.Context, t *testing.T, p *pgxpkg.Pool, recordID string) {
	t.Helper()
	// recContacts can take two live claim/re-score cycles (retry exhausts,
	// then a nudge fires), each gated by the harness's ~10s retry/nudge
	// delay, so this needs more headroom than the single-hop pipelineWait.
	deadline := time.Now().Add(45 * time.Second)
	var last string
	stableSince := time.Time{}
	for {
		var s string
		err := p.QueryRow(ctx,
			`SELECT current_state FROM record_state WHERE record_id = $1`, recordID,
		).Scan(&s)
		if err == nil {
			if s != last {
				last = s
				stableSince = time.Now()
			} else if !stableSince.IsZero() && time.Since(stableSince) > 1500*time.Millisecond {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("record %s did not settle within %s (last state=%s)", recordID, pipelineWait, last)
		}
		time.Sleep(200 * time.Millisecond)
	}
}
