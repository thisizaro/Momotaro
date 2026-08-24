package engine

import (
	"testing"
	"time"

	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

// testGuardrails is a deliberately small, round configuration so a test that
// fails names the rule it broke rather than an arithmetic coincidence.
var testGuardrails = GuardrailConfig{
	MaxRetries:      3,
	MaxContacts:     2,
	ContactCooldown: 24 * time.Hour,
	RecoveryWindow:  7 * 24 * time.Hour,
}

var (
	testNow     = time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	testCreated = testNow.Add(-1 * time.Hour) // young record, window wide open
)

func freshHistory() attemptHistory {
	return attemptHistory{RecordCreatedAt: testCreated}
}

func timePtr(t time.Time) *time.Time { return &t }

// everySpendingAction is every action that costs money. ESCALATE and NONE are
// excluded on purpose: they spend nothing and must never be blocked, which is
// what makes them the safety valve.
var everySpendingAction = []commonv1.ActionType{
	commonv1.ActionType_ACTION_TYPE_RETRY,
	commonv1.ActionType_ACTION_TYPE_NUDGE_METHOD_UPDATE,
	commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER,
}

func TestGuardrailsPermitEverySpendingActionForAFreshRecord(t *testing.T) {
	v := applyGuardrails(freshHistory(), testGuardrails, testNow)

	for _, action := range everySpendingAction {
		if !v.allows(action) {
			t.Errorf("fresh record: %s blocked (%q), want permitted", action, v.reason(action))
		}
	}
}

func TestGuardrailsBlockRetryOnceTheRetryBudgetIsSpent(t *testing.T) {
	tests := []struct {
		name    string
		retries int
		want    bool
	}{
		{name: "one under the cap", retries: testGuardrails.MaxRetries - 1, want: true},
		{name: "exactly at the cap", retries: testGuardrails.MaxRetries, want: false},
		{name: "past the cap", retries: testGuardrails.MaxRetries + 1, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := freshHistory()
			h.Retries = tt.retries

			if got := applyGuardrails(h, testGuardrails, testNow).allows(commonv1.ActionType_ACTION_TYPE_RETRY); got != tt.want {
				t.Errorf("retries=%d: allows(RETRY) = %v, want %v", tt.retries, got, tt.want)
			}
		})
	}
}

func TestGuardrailsBlockNudgesOnceTheContactCapIsReached(t *testing.T) {
	h := freshHistory()
	h.Contacts = testGuardrails.MaxContacts

	v := applyGuardrails(h, testGuardrails, testNow)
	for _, action := range []commonv1.ActionType{
		commonv1.ActionType_ACTION_TYPE_NUDGE_METHOD_UPDATE,
		commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER,
	} {
		if v.allows(action) {
			t.Errorf("contacts at cap: %s permitted, want blocked", action)
		}
	}
}

func TestGuardrailsBlockNudgesDuringTheContactCooldownAndReleaseThemAfter(t *testing.T) {
	tests := []struct {
		name  string
		since time.Duration // how long ago the last contact was
		want  bool
	}{
		{name: "just contacted", since: time.Minute, want: false},
		{name: "one second short of the cooldown", since: testGuardrails.ContactCooldown - time.Second, want: false},
		{name: "cooldown exactly elapsed", since: testGuardrails.ContactCooldown, want: true},
		{name: "well past the cooldown", since: testGuardrails.ContactCooldown + time.Hour, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := freshHistory()
			h.Contacts = 1 // under the cap, so cooldown is the only thing in play
			h.LastContactAt = timePtr(testNow.Add(-tt.since))

			if got := applyGuardrails(h, testGuardrails, testNow).allows(commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER); got != tt.want {
				t.Errorf("last contact %v ago: allows(NUDGE_REMINDER) = %v, want %v", tt.since, got, tt.want)
			}
		})
	}
}

// The cooldown gates contacts only. A retry costs the customer no attention,
// so it must not be held back by when we last messaged them.
func TestGuardrailsContactCooldownDoesNotBlockRetry(t *testing.T) {
	h := freshHistory()
	h.Contacts = 1
	h.LastContactAt = timePtr(testNow.Add(-time.Minute))

	if !applyGuardrails(h, testGuardrails, testNow).allows(commonv1.ActionType_ACTION_TYPE_RETRY) {
		t.Error("retry blocked by a contact cooldown, want permitted: the two budgets are independent")
	}
}

// PRD section 11: no action once a record is past its recovery window.
func TestGuardrailsBlockEverySpendingActionPastTheRecoveryWindow(t *testing.T) {
	h := freshHistory()
	h.RecordCreatedAt = testNow.Add(-testGuardrails.RecoveryWindow - time.Hour)

	v := applyGuardrails(h, testGuardrails, testNow)
	for _, action := range everySpendingAction {
		if v.allows(action) {
			t.Errorf("past recovery window: %s permitted, want blocked", action)
		}
	}
}

func TestGuardrailsKeepSpendingPermittedInsideTheRecoveryWindow(t *testing.T) {
	h := freshHistory()
	h.RecordCreatedAt = testNow.Add(-testGuardrails.RecoveryWindow + time.Hour)

	if !applyGuardrails(h, testGuardrails, testNow).allows(commonv1.ActionType_ACTION_TYPE_RETRY) {
		t.Error("retry blocked one hour before the recovery window closes, want permitted")
	}
}

// The safety valve. A record whose every budget is spent must still be able to
// reach a human, or PRD section 11's "never loops forever" has no exit.
func TestGuardrailsAlwaysPermitEscalateAndNone(t *testing.T) {
	h := attemptHistory{
		Retries:         testGuardrails.MaxRetries + 5,
		Contacts:        testGuardrails.MaxContacts + 5,
		LastContactAt:   timePtr(testNow),
		RecordCreatedAt: testNow.Add(-testGuardrails.RecoveryWindow * 10),
	}

	v := applyGuardrails(h, testGuardrails, testNow)
	for _, action := range []commonv1.ActionType{
		commonv1.ActionType_ACTION_TYPE_ESCALATE,
		commonv1.ActionType_ACTION_TYPE_NONE,
	} {
		if !v.allows(action) {
			t.Errorf("every budget exhausted: %s blocked (%q), want always permitted", action, v.reason(action))
		}
	}
}

// Guardrails may only ever remove options (ARCHITECTURE.md section 5a). A
// blocked action must carry a reason, because that reason is what the audit
// trail records as the justification for the downgrade.
func TestGuardrailsGiveEveryBlockedActionAReason(t *testing.T) {
	h := freshHistory()
	h.Retries = testGuardrails.MaxRetries
	h.Contacts = testGuardrails.MaxContacts

	v := applyGuardrails(h, testGuardrails, testNow)
	for _, action := range everySpendingAction {
		if v.allows(action) {
			t.Fatalf("%s permitted, test cannot check its reason", action)
		}
		if v.reason(action) == "" {
			t.Errorf("%s blocked with an empty reason, want an explanation for the audit trail", action)
		}
	}
}

func TestPermittedOrEscalateKeepsAPermittedAction(t *testing.T) {
	v := applyGuardrails(freshHistory(), testGuardrails, testNow)

	got, reason := permittedOrEscalate(commonv1.ActionType_ACTION_TYPE_RETRY, v)
	if got != commonv1.ActionType_ACTION_TYPE_RETRY {
		t.Errorf("permittedOrEscalate(RETRY) = %s, want RETRY unchanged", got)
	}
	if reason != "" {
		t.Errorf("permitted action carried a downgrade reason %q, want none", reason)
	}
}

// PRD section 11: a record that can no longer be acted on downgrades to
// "needs human" rather than looping.
func TestPermittedOrEscalateDowngradesABlockedActionAndSaysWhy(t *testing.T) {
	h := freshHistory()
	h.Retries = testGuardrails.MaxRetries

	v := applyGuardrails(h, testGuardrails, testNow)
	got, reason := permittedOrEscalate(commonv1.ActionType_ACTION_TYPE_RETRY, v)

	if got != commonv1.ActionType_ACTION_TYPE_ESCALATE {
		t.Errorf("permittedOrEscalate(RETRY) with the budget spent = %s, want ESCALATE", got)
	}
	if reason == "" {
		t.Error("downgrade carried no reason, want the guardrail's explanation for the audit trail")
	}
}

// The zero value is the most restrictive setting there is, not a harmless
// default, so it must be refused at startup rather than silently escalating
// every record (docs/INCIDENTS.md 2026-08-24).
func TestGuardrailConfigValidateRejectsAnythingNonPositive(t *testing.T) {
	tests := []struct {
		name string
		cfg  GuardrailConfig
	}{
		{name: "the zero value", cfg: GuardrailConfig{}},
		{name: "no retry budget", cfg: GuardrailConfig{MaxRetries: 0, MaxContacts: 2, ContactCooldown: time.Hour, RecoveryWindow: time.Hour}},
		{name: "no contact budget", cfg: GuardrailConfig{MaxRetries: 3, MaxContacts: 0, ContactCooldown: time.Hour, RecoveryWindow: time.Hour}},
		{name: "no cooldown", cfg: GuardrailConfig{MaxRetries: 3, MaxContacts: 2, ContactCooldown: 0, RecoveryWindow: time.Hour}},
		{name: "no recovery window", cfg: GuardrailConfig{MaxRetries: 3, MaxContacts: 2, ContactCooldown: time.Hour, RecoveryWindow: 0}},
		{name: "negative window", cfg: GuardrailConfig{MaxRetries: 3, MaxContacts: 2, ContactCooldown: time.Hour, RecoveryWindow: -time.Hour}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.cfg.Validate(); err == nil {
				t.Errorf("Validate() accepted %+v, want an error", tt.cfg)
			}
		})
	}
}

func TestGuardrailConfigValidateAcceptsASaneConfig(t *testing.T) {
	if err := testGuardrails.Validate(); err != nil {
		t.Errorf("Validate() rejected a sane config: %v", err)
	}
}

// Guards against the production list and this file's expectations drifting
// apart: an action added to the menu that nobody adds here would escape the
// guardrails entirely while every test still passed.
func TestSpendingActionsIsExactlyTheActionsThatCostMoney(t *testing.T) {
	if len(spendingActions) != len(everySpendingAction) {
		t.Fatalf("spendingActions has %d entries, this file expects %d", len(spendingActions), len(everySpendingAction))
	}
	want := make(map[commonv1.ActionType]bool, len(everySpendingAction))
	for _, a := range everySpendingAction {
		want[a] = true
	}
	for _, a := range spendingActions {
		if !want[a] {
			t.Errorf("spendingActions contains %s, which this file does not expect to cost money", a)
		}
	}
}
