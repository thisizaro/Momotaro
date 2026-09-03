// The deterministic safety layer: retry budgets, contact caps, cooldowns and
// the recovery window. Pure policy, no I/O, so every rule is testable on its
// own (docs/ENGINEERING.md section 14).
//
// Guardrails run AFTER the Classifier answers and BEFORE the economics scorer
// picks, and they may only ever REMOVE options, never add one
// (docs/ARCHITECTURE.md section 5a). That ordering is what lets us say the
// model proposes but never decides how money is spent.
//
// These caps do double duty (docs/PRD.md section 2b): regulatory compliance
// with NPCI-style mandate limits, and protection of the merchant's standing
// with issuers, since hammering a card with retries degrades authorization
// rates on future legitimate payments. That is why they are deterministic and
// not something a model can talk its way past.
//
// Enforcement is Postgres-transactional and its counters are derived from
// INTERVENTION_ATTEMPT, deliberately not Redis: a cap enforced by a cache is
// not enforced, because an unreachable cache falls through and a fallen
// through cap check is exactly the stopping-rule violation docs/PRD.md
// section 9 calls impossible. See docs/DECISIONS.md (2026-08-24).
package engine

import (
	"fmt"
	"time"

	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

// GuardrailConfig holds the hard limits from docs/PRD.md section 11. These
// are compliance and asset-protection limits, deliberately kept separate from
// the economics cost model in configs/: both are numbers, but a cap is a rule
// we must not break and a cost is an input to a judgement.
type GuardrailConfig struct {
	MaxRetries      int           // hard cap on debit re-attempts per record
	MaxContacts     int           // hard cap on customer contacts per record
	ContactCooldown time.Duration // minimum gap between two contacts
	RecoveryWindow  time.Duration // no spending once a record is older than this
}

// attemptHistory is everything the guardrails need to know about what has
// already been spent on one record. Counts are derived from
// INTERVENTION_ATTEMPT rather than stored as columns, so they cannot drift
// from the history the audit trail is the source of truth for.
type attemptHistory struct {
	Retries         int
	Contacts        int
	LastContactAt   *time.Time // nil when the customer has never been contacted
	RecordCreatedAt time.Time
}

// spendingActions are the actions that cost money and are therefore subject
// to the guardrails. ESCALATE and NONE are absent on purpose: they spend
// nothing, and keeping them always available is what guarantees a record with
// every budget exhausted still has somewhere to go.
var spendingActions = []commonv1.ActionType{
	commonv1.ActionType_ACTION_TYPE_RETRY,
	commonv1.ActionType_ACTION_TYPE_NUDGE_METHOD_UPDATE,
	commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER,
}

// guardrailVerdict is the set of actions the guardrails removed, each with the
// reason it was removed. Only blocked actions appear; anything absent is
// permitted. The reason is not decoration: it is what the audit trail records
// as the justification for a downgrade.
type guardrailVerdict struct {
	blocked map[commonv1.ActionType]string
	// retryHeldByDowntime is true when RETRY was blocked SPECIFICALLY by an
	// active payment downtime (applyDowntimeGuardrail, docs/PHASE5_5_IMPLEMENTATION.md
	// Unit Y), as opposed to the retry budget, the recovery window, or any
	// other permanent stop. It is the signal scoreAndRoute uses to decide
	// whether an otherwise-unpermitted retry should be DEFERRED (scheduled
	// normally, to run once the downtime clears) rather than escalated: a
	// downtime is bad timing, not a reason to give up on the record.
	retryHeldByDowntime bool
}

// allows reports whether the guardrails permit action.
func (v guardrailVerdict) allows(action commonv1.ActionType) bool {
	_, blocked := v.blocked[action]
	return !blocked
}

// reason explains why action was blocked, or "" if it was permitted.
func (v guardrailVerdict) reason(action commonv1.ActionType) string {
	return v.blocked[action]
}

// applyGuardrails evaluates every rule against one record's history.
func applyGuardrails(h attemptHistory, cfg GuardrailConfig, now time.Time) guardrailVerdict {
	v := guardrailVerdict{blocked: make(map[commonv1.ActionType]string)}

	// The recovery window subsumes the rest: past it nothing may be spent, so
	// reporting a retry cap alongside it would describe a rule that is no
	// longer the operative one.
	if reason, closed := recoveryWindowClosed(h, cfg, now); closed {
		for _, action := range spendingActions {
			v.blocked[action] = reason
		}
		return v
	}

	if reason, spent := retryBudgetSpent(h, cfg); spent {
		v.blocked[commonv1.ActionType_ACTION_TYPE_RETRY] = reason
	}
	if reason, blocked := contactBlocked(h, cfg, now); blocked {
		v.blocked[commonv1.ActionType_ACTION_TYPE_NUDGE_METHOD_UPDATE] = reason
		v.blocked[commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER] = reason
	}
	return v
}

// recoveryWindowClosed reports whether the record is too old to act on at all
// (docs/PRD.md section 11: no action outside a defined time window).
func recoveryWindowClosed(h attemptHistory, cfg GuardrailConfig, now time.Time) (string, bool) {
	age := now.Sub(h.RecordCreatedAt)
	if age < cfg.RecoveryWindow {
		return "", false
	}
	return fmt.Sprintf("recovery window closed: record is %s old, window is %s", age, cfg.RecoveryWindow), true
}

// retryBudgetSpent reports whether this record has used its debit re-attempts.
func retryBudgetSpent(h attemptHistory, cfg GuardrailConfig) (string, bool) {
	if h.Retries < cfg.MaxRetries {
		return "", false
	}
	return fmt.Sprintf("retry budget exhausted: %d of %d attempts used", h.Retries, cfg.MaxRetries), true
}

// contactBlocked reports whether the customer may be messaged right now,
// covering both limits that gate contact: the hard cap and the cooldown.
func contactBlocked(h attemptHistory, cfg GuardrailConfig, now time.Time) (string, bool) {
	if h.Contacts >= cfg.MaxContacts {
		return fmt.Sprintf("contact cap reached: %d of %d contacts used", h.Contacts, cfg.MaxContacts), true
	}
	if h.LastContactAt == nil {
		return "", false
	}
	if since := now.Sub(*h.LastContactAt); since < cfg.ContactCooldown {
		return fmt.Sprintf("contact cooldown active: last contact %s ago, cooldown is %s", since, cfg.ContactCooldown), true
	}
	return "", false
}

// permittedOrEscalate downgrades a blocked action to ESCALATE, which is
// docs/PRD.md section 11's rule that a record which can no longer be acted on
// becomes a human's problem rather than looping forever. The returned reason
// is empty when nothing was downgraded.
//
// This is the Phase 1-shaped caller's entry point. Once the economics scorer
// lands it picks from the permitted set directly (docs/ARCHITECTURE.md
// section 5a), and this stays as the fallback for an empty set.
func permittedOrEscalate(want commonv1.ActionType, v guardrailVerdict) (commonv1.ActionType, string) {
	if v.allows(want) {
		return want, ""
	}
	return commonv1.ActionType_ACTION_TYPE_ESCALATE, v.reason(want)
}

// Validate rejects a GuardrailConfig that cannot be meant. Every field is
// required and must be positive.
//
// This exists because the zero value is not a harmless default, it is the most
// restrictive setting there is: a RecoveryWindow of 0 makes every record
// instantly "past its window", so the guardrails downgrade every single action
// to ESCALATE and the agent silently stops doing anything at all while still
// reporting success. That is a worse failure than refusing to start, and it is
// exactly what happened when this layer was first wired in
// (docs/INCIDENTS.md 2026-08-24). Config is validated at startup so it cannot
// happen in a deployment (docs/ENGINEERING.md section 11, item 6).
func (c GuardrailConfig) Validate() error {
	if c.MaxRetries <= 0 {
		return fmt.Errorf("MAX_RETRIES must be positive, got %d", c.MaxRetries)
	}
	if c.MaxContacts <= 0 {
		return fmt.Errorf("MAX_CONTACTS must be positive, got %d", c.MaxContacts)
	}
	if c.ContactCooldown <= 0 {
		return fmt.Errorf("CONTACT_COOLDOWN must be positive, got %s", c.ContactCooldown)
	}
	if c.RecoveryWindow <= 0 {
		return fmt.Errorf("RECOVERY_WINDOW must be positive, got %s", c.RecoveryWindow)
	}
	return nil
}

// permittedActions is the set of spending actions the guardrails allow, which
// is what the economics scorer chooses from (docs/ARCHITECTURE.md section 5a:
// guardrails filter, then economics decides). Order follows spendingActions so
// the scorer's tie-breaking is deterministic rather than map-iteration order.
//
// ESCALATE and NONE are deliberately absent. They are not economic choices:
// escalation is the fallback reached when nothing is permitted or nothing is
// worth doing, and it must never win a comparison against a real intervention.
func permittedActions(v guardrailVerdict) []commonv1.ActionType {
	permitted := make([]commonv1.ActionType, 0, len(spendingActions))
	for _, action := range spendingActions {
		if v.allows(action) {
			permitted = append(permitted, action)
		}
	}
	return permitted
}

// attemptNumberFor is the action's OWN next attempt number, which is what the
// prior table is keyed on: it asks how well a second retry does, a different
// question from how well anything does after two interventions.
func attemptNumberFor(action commonv1.ActionType, h attemptHistory) int {
	if action == commonv1.ActionType_ACTION_TYPE_RETRY {
		return h.Retries + 1
	}
	return h.Contacts + 1
}

// downtimeStatus is what the guardrails need to know about the most recent
// UNRESOLVED Razorpay payment-downtime event covering a record's instrument
// (docs/PHASE5_5_IMPLEMENTATION.md Unit Y). Loaded by the store from
// PAYMENT_DOWNTIME, keyed on instrument_ref, the same pattern attemptHistory
// uses so this stays a plain value and every function that reasons about it
// stays pure and testable without Postgres.
//
// Present is false whenever there is no unresolved downtime row for this
// instrument at all: an empty instrument_ref, an instrument nothing has ever
// reported downtime for, or one whose only rows are already resolved all
// look this up trivially and land here.
type downtimeStatus struct {
	Present    bool
	DowntimeID string
	Method     string // e.g. "netbanking", "card", "upi", exactly as Razorpay sent it
	// Instrument is the single identifying value pulled out of Razorpay's
	// `instrument` object (which varies by method, e.g. {"bank":"VIJB"} for
	// netbanking vs {"issuer":"SBIN","type":"credit"} for a card): whichever
	// field its own instrument_schema names first. See the Gateway's
	// downtimeInstrumentKey, which does this extraction once at the edge so
	// nothing downstream needs to know Razorpay's per-method shapes.
	Instrument string
	Severity   string // "high" | "medium" in Razorpay's documented examples, but treated as an open string: an unrecognised value is still shown, never rejected
	Scheduled  bool   // true for planned maintenance, false for an unplanned outage
	Begin      time.Time
	End        *time.Time // nil while the downtime is ongoing (Razorpay's own null-while-ongoing shape)
}

// downtimeMaxUnresolvedHold caps how long an UNPLANNED downtime (Scheduled
// false, End nil) can hold a retry back without a payment.downtime.resolved
// event ever arriving. A SCHEDULED downtime needs no such cap: Razorpay
// publishes its End up front, so that timestamp alone bounds it once it
// passes, resolved event or not.
//
// This is the safety valve behind "must not strand a record permanently"
// (docs/PHASE5_5_IMPLEMENTATION.md Unit Y): a webhook can be lost, a demo
// operator can forget to resolve what they raised, and this service can
// restart mid-outage with nothing but this Postgres row to go on (there is
// no in-memory timer to lose). Six hours comfortably covers a real
// unscheduled outage without leaving a record parked for the length of a
// working day on a signal that may itself have gone silent.
const downtimeMaxUnresolvedHold = 6 * time.Hour

// downtimeBlocksRetry reports whether d is, right now, an active downtime
// that should hold RETRY back, and if so, the human-readable reason the
// audit trail records for it (docs/PHASE5_5_IMPLEMENTATION.md Unit Y: "bank
// downtime active: netbanking VIJB, severity high", matching the shape of
// every other guardrail reason in this file).
func downtimeBlocksRetry(d downtimeStatus, now time.Time) (string, bool) {
	if !d.Present {
		return "", false
	}
	if now.Before(d.Begin) {
		return "", false
	}
	if d.End != nil {
		if !now.Before(*d.End) {
			return "", false // a scheduled downtime's own published end has passed
		}
	} else if !now.Before(d.Begin.Add(downtimeMaxUnresolvedHold)) {
		return "", false // the safety valve: no resolved event, but this has run long enough
	}
	return fmt.Sprintf("bank downtime active: %s %s, severity %s", d.Method, d.Instrument, d.Severity), true
}

// applyDowntimeGuardrail layers the downtime rule onto a verdict
// applyGuardrails already computed. Kept as a separate function, rather than
// a new parameter threaded through applyGuardrails itself, so the existing
// guardrail rules (retry budget, contact cap/cooldown, recovery window) and
// every test written against them stay exactly as they are; scoreAndRoute is
// the one place that calls both and merges the result.
//
// If RETRY is already blocked for a permanent reason (the retry budget is
// spent, or the recovery window has closed), that verdict is left untouched:
// a downtime clearing would not make either of those reasons any less true,
// and overwriting a permanent reason with a temporary one would misreport
// why the record really cannot retry.
func applyDowntimeGuardrail(v guardrailVerdict, d downtimeStatus, now time.Time) guardrailVerdict {
	if !v.allows(commonv1.ActionType_ACTION_TYPE_RETRY) {
		return v
	}
	reason, blocked := downtimeBlocksRetry(d, now)
	if !blocked {
		return v
	}
	v.blocked[commonv1.ActionType_ACTION_TYPE_RETRY] = reason
	v.retryHeldByDowntime = true
	return v
}
