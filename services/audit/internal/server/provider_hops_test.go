//go:build integration

// Phase 3 Unit E: audit_entry.provider_hops survives the round trip from the
// column into AuditEntry.hops, in order.
//
// Integration tier because the whole point is the SQL and the codec agreeing;
// a unit test over hopcodec alone (internal/platform/hopcodec) already proves
// the encoding, and would prove nothing about whether this service actually
// selects the column.
package server

import (
	"context"
	"testing"

	auditv1 "github.com/thisizaro/Momotaro/proto/gen/audit/v1"
)

func TestGetRecordAuditReturnsProviderHopsInAttemptOrder(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	batchID, recordID := seedRecord(ctx, t, pool)
	seedRecordState(ctx, t, pool, recordID, "RECORD_STATE_ESCALATED")

	// A real fallback: the primary was throttled, the failover answered. This
	// is the shape PRD.md section 12 step 5 asks a judge to look at, and the
	// shape `source` alone cannot express, since SOURCE_LLM reads identically
	// whether or not a fallback happened.
	if _, err := pool.Exec(ctx, `
		INSERT INTO audit_entry (record_id, batch_id, ts, from_state, to_state, reason, rationale, source, provider_hops, actor, attempt_number)
		VALUES
		 ($1, $2, now() - interval '2 minutes', 'RECORD_STATE_NEW', 'RECORD_STATE_SCORING', 'classified, guardrails applied, scoring', 'risk hold', 'SOURCE_LLM', 'groq:rate_limited,gemini:ok', 'system', 0),
		 ($1, $2, now() - interval '1 minute', 'RECORD_STATE_SCORING', 'RECORD_STATE_ESCALATED', 'classifier recommended escalation', 'risk hold', 'SOURCE_LLM', 'groq:rate_limited,gemini:ok', 'system', 0)
	`, recordID, batchID); err != nil {
		t.Fatalf("seed audit_entry: %v", err)
	}

	s := New(pool)
	resp, err := s.GetRecordAudit(ctx, &auditv1.GetRecordAuditRequest{RecordId: recordID})
	if err != nil {
		t.Fatalf("GetRecordAudit: %v", err)
	}
	if len(resp.Entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(resp.Entries))
	}

	for i, e := range resp.Entries {
		hops := e.GetHops()
		if len(hops) != 2 {
			t.Fatalf("entry %d has %d hops, want 2", i, len(hops))
		}
		// Order is the story: which was tried first, and what it did.
		if hops[0].GetProvider() != "groq" || hops[0].GetResult() != "rate_limited" {
			t.Errorf("entry %d hop 0 = %s/%s, want groq/rate_limited", i, hops[0].GetProvider(), hops[0].GetResult())
		}
		if hops[1].GetProvider() != "gemini" || hops[1].GetResult() != "ok" {
			t.Errorf("entry %d hop 1 = %s/%s, want gemini/ok", i, hops[1].GetProvider(), hops[1].GetResult())
		}
	}
}

// NULL means no classification happened behind this transition. It must come
// back as an empty list, not a one-element list of empty strings, or every
// consumer has to special-case it (migration 00005).
func TestGetRecordAuditReturnsNoHopsForATransitionThatDidNotClassify(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	batchID, recordID := seedRecord(ctx, t, pool)
	seedRecordState(ctx, t, pool, recordID, "RECORD_STATE_ESCALATED")

	if _, err := pool.Exec(ctx, `
		INSERT INTO audit_entry (record_id, batch_id, ts, from_state, to_state, reason, actor, attempt_number)
		VALUES ($1, $2, now(), 'RECORD_STATE_NEW', 'RECORD_STATE_ESCALATED', 'classifier recommended escalation', 'system', 0)
	`, recordID, batchID); err != nil {
		t.Fatalf("seed audit_entry: %v", err)
	}

	s := New(pool)
	resp, err := s.GetRecordAudit(ctx, &auditv1.GetRecordAuditRequest{RecordId: recordID})
	if err != nil {
		t.Fatalf("GetRecordAudit: %v", err)
	}
	if got := resp.Entries[0].GetHops(); len(got) != 0 {
		t.Errorf("hops = %+v, want empty for a NULL provider_hops", got)
	}
}
