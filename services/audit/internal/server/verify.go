package server

import (
	"fmt"

	auditv1 "github.com/thisizaro/Momotaro/proto/gen/audit/v1"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

// maxExamples bounds VerifyInvariantsResponse.examples so a systemic bug
// cannot produce an unbounded response (proto/audit/v1/audit.proto).
const maxExamples = 20

// verifyInvariants applies the correctness invariants (docs/ARCHITECTURE.md
// section 10a) to a batch of record snapshots and aggregates the result.
// Pure and DB-free by design: store.go's scanRecords is the only thing that
// touches SQL for this RPC, so this logic is fast and exhaustively testable
// without Postgres (docs/ENGINEERING.md section 14).
//
// Each record contributes at most one violation, even if it technically has
// more than one problem: the examples map is keyed by record_id, so a
// second reason would only overwrite the first anyway. An incomplete trail
// is checked, and reported, ahead of transition validity.
func verifyInvariants(snapshots []recordSnapshot) *auditv1.VerifyInvariantsResponse {
	resp := &auditv1.VerifyInvariantsResponse{
		RecordsChecked: int64(len(snapshots)),
		Examples:       map[string]string{},
	}

	for _, snap := range snapshots {
		if !snap.HasState {
			// Ingested but not yet touched by Decision Engine. Normal,
			// see docs/ARCHITECTURE.md section 10a.
			continue
		}

		if reason, bad := incompleteTrail(snap); bad {
			resp.IncompleteAuditTrails++
			addExample(resp.Examples, snap.RecordID, reason)
			continue
		}

		if reason, bad := impossibleTransition(snap); bad {
			resp.ImpossibleTransitions++
			addExample(resp.Examples, snap.RecordID, reason)
		}

		// stopping_rule_violations is deliberately always zero here: no
		// retry/contact caps exist yet (docs/PLAN.md Phase 2 economics),
		// so there is nothing to check against. Wiring a check against a
		// rule that does not exist would be fabricated, not verified
		// (docs/ENGINEERING.md section 13).
	}
	return resp
}

// trailComplete reports whether snap's history is a sound account of how it
// reached its current state: every state change has an entry, the entries
// chain, and every edge is legal.
//
// This is exactly what GetRecordAuditResponse.trail_complete claims, so it
// runs the same two checks VerifyInvariants aggregates rather than a second
// implementation that could drift. It previously did not exist at all and
// the field was hardcoded true, which made the end-to-end test's assertion
// on it vacuous (docs/INCIDENTS.md 2026-08-23).
func trailComplete(snap recordSnapshot) bool {
	if !snap.HasState {
		// Ingested but not yet picked up by Decision Engine. No state change
		// has happened, so there is no trail that could be missing one.
		return true
	}
	if _, bad := incompleteTrail(snap); bad {
		return false
	}
	if _, bad := impossibleTransition(snap); bad {
		return false
	}
	return true
}

// snapshotFor builds the verifier's view of one record out of what
// GetRecordAudit has already loaded, so that RPC can answer trail_complete
// without a second trip to Postgres and without its own notion of what
// "complete" means.
func snapshotFor(recordID string, currentState commonv1.RecordState, entries []*auditv1.AuditEntry) recordSnapshot {
	snap := recordSnapshot{
		RecordID: recordID,
		// store.go's loadCurrentState reports UNSPECIFIED for a record with
		// no RECORD_STATE row yet, which is the same fact HasState=false
		// carries to the verifier.
		HasState:     currentState != commonv1.RecordState_RECORD_STATE_UNSPECIFIED,
		CurrentState: currentState,
	}
	for _, e := range entries {
		snap.Entries = append(snap.Entries, transition{From: e.GetFromState(), To: e.GetToState()})
	}
	return snap
}

// incompleteTrail reports a record whose RECORD_STATE and AUDIT_ENTRY trail
// disagree: either no entry exists at all, or the latest entry's to_state
// does not match the persisted current_state. Both mean the transactional
// write rule (docs/ARCHITECTURE.md section 10a) was broken somewhere.
func incompleteTrail(snap recordSnapshot) (reason string, bad bool) {
	if len(snap.Entries) == 0 {
		return fmt.Sprintf("current_state=%s but zero audit entries", snap.CurrentState), true
	}
	last := snap.Entries[len(snap.Entries)-1]
	if last.To != snap.CurrentState {
		return fmt.Sprintf("record_state says %s but the last audit entry says %s", snap.CurrentState, last.To), true
	}
	return "", false
}

// impossibleTransition reports the first edge in snap's trail that either
// does not chain from the previous entry (a gap) or is not a valid move in
// the state machine (statemachine.go).
func impossibleTransition(snap recordSnapshot) (reason string, bad bool) {
	for i, t := range snap.Entries {
		if i > 0 && t.From != snap.Entries[i-1].To {
			return fmt.Sprintf("entry %d: from=%s does not match the previous entry's to=%s", i, t.From, snap.Entries[i-1].To), true
		}
		if !isAllowedTransition(t.From, t.To) {
			return fmt.Sprintf("entry %d: %s -> %s is not a valid transition", i, t.From, t.To), true
		}
	}
	return "", false
}

func addExample(examples map[string]string, recordID, reason string) {
	if len(examples) >= maxExamples {
		return
	}
	examples[recordID] = reason
}
