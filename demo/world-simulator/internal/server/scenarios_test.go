package server

import (
	"context"
	"math/rand"
	"testing"

	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	worldsimv1 "github.com/thisizaro/Momotaro/proto/gen/worldsim/v1"
)

func TestScenarioByNameKnownNames(t *testing.T) {
	for _, name := range []string{"", "normal", "bank-outage", "salary-day", "dead-cards"} {
		if _, ok := scenarioByName(name); !ok {
			t.Errorf("scenarioByName(%q): not found, want it resolved", name)
		}
	}
}

func TestScenarioByNameEmptyIsNormal(t *testing.T) {
	got, ok := scenarioByName("")
	if !ok {
		t.Fatal("scenarioByName(\"\"): not found")
	}
	want, _ := scenarioByName("normal")
	if got.Name != want.Name {
		t.Errorf("scenarioByName(\"\") = %q, want %q (normal)", got.Name, want.Name)
	}
}

func TestScenarioByNameUnknownNotFound(t *testing.T) {
	if _, ok := scenarioByName("not-a-real-scenario"); ok {
		t.Error("scenarioByName(\"not-a-real-scenario\"): found, want not found")
	}
}

func TestListScenariosReturnsAllFourWithDescriptions(t *testing.T) {
	got := listScenarios()
	if len(got) != 4 {
		t.Fatalf("listScenarios() returned %d presets, want 4", len(got))
	}
	seen := map[string]bool{}
	for _, p := range got {
		if p.Description == "" {
			t.Errorf("preset %q has no description", p.Name)
		}
		seen[p.Name] = true
	}
	for _, want := range []string{"normal", "bank-outage", "salary-day", "dead-cards"} {
		if !seen[want] {
			t.Errorf("listScenarios() missing %q", want)
		}
	}
}

// generateScenarioRecord must never duplicate the vocabulary that
// services/classifier/internal/rules/buckets.go already owns: every code a
// scenario forces must be one of Razorpay's real, published codes, verified
// by hand against that table (Unit W cannot import it: it is compiler-
// private to the classifier service, docs/ARCHITECTURE.md section 2a).
func TestGenerateScenarioRecordBankOutageConcentratesOnRealBankFailureCode(t *testing.T) {
	preset, _ := scenarioByName("bank-outage")
	rng := rand.New(rand.NewSource(1))
	matched := 0
	const n = 1000
	for i := 0; i < n; i++ {
		rec := generateScenarioRecord(rng, preset)
		if rec.TrueBucket != commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK {
			continue
		}
		if rec.FailureCode == "BANK_NOT_AVAILABLE" {
			matched++
		}
	}
	if matched < n/2 {
		t.Errorf("bank-outage: only %d/%d records concentrated on BANK_NOT_AVAILABLE, want a clear majority", matched, n)
	}
}

func TestGenerateScenarioRecordSalaryDayConcentratesOnInsufficientFunds(t *testing.T) {
	preset, _ := scenarioByName("salary-day")
	rng := rand.New(rand.NewSource(2))
	matched := 0
	const n = 1000
	for i := 0; i < n; i++ {
		rec := generateScenarioRecord(rng, preset)
		if rec.TrueBucket == commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_INSUFFICIENT_FUNDS && rec.FailureCode == "INSUFFICIENT_FUNDS" {
			matched++
		}
	}
	if matched < n/2 {
		t.Errorf("salary-day: only %d/%d records concentrated on INSUFFICIENT_FUNDS, want a clear majority", matched, n)
	}
}

func TestGenerateScenarioRecordDeadCardsConcentratesOnRealHardDeclineCodes(t *testing.T) {
	preset, _ := scenarioByName("dead-cards")
	rng := rand.New(rand.NewSource(3))
	matched := 0
	const n = 1000
	for i := 0; i < n; i++ {
		rec := generateScenarioRecord(rng, preset)
		if rec.TrueBucket != commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_HARD_DECLINE {
			continue
		}
		if rec.FailureCode == "CARD_EXPIRED" || rec.FailureCode == "DEBIT_INSTRUMENT_BLOCKED" {
			matched++
		}
	}
	if matched < n/2 {
		t.Errorf("dead-cards: only %d/%d records concentrated on CARD_EXPIRED/DEBIT_INSTRUMENT_BLOCKED, want a clear majority", matched, n)
	}
}

// The "normal" preset must not force anything: it is a pass-through onto
// syntheticgen's own default distribution, so it must produce every bucket
// across enough draws, not concentrate like the other three.
func TestGenerateScenarioRecordNormalIsNotConcentrated(t *testing.T) {
	preset, _ := scenarioByName("normal")
	rng := rand.New(rand.NewSource(4))
	buckets := map[commonv1.RootCauseBucket]int{}
	const n = 1000
	for i := 0; i < n; i++ {
		rec := generateScenarioRecord(rng, preset)
		buckets[rec.TrueBucket]++
	}
	if len(buckets) < 4 {
		t.Errorf("normal scenario only produced %d distinct buckets across %d draws, want a real spread: %v", len(buckets), n, buckets)
	}
}

// Every generated record, scenario or not, must still be a valid record:
// generateScenarioRecord only overrides fields, it never leaves one
// unset.
func TestGenerateScenarioRecordAlwaysValid(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	for _, name := range []string{"normal", "bank-outage", "salary-day", "dead-cards"} {
		preset, _ := scenarioByName(name)
		for i := 0; i < 200; i++ {
			rec := generateScenarioRecord(rng, preset)
			if rec.Type == commonv1.RecordType_RECORD_TYPE_UNSPECIFIED {
				t.Fatalf("%s: record %d has UNSPECIFIED Type", name, i)
			}
			if rec.FailureCode == "" {
				t.Fatalf("%s: record %d has empty FailureCode", name, i)
			}
			if rec.AmountPaise <= 0 {
				t.Fatalf("%s: record %d has non-positive AmountPaise", name, i)
			}
			if rec.TrueBucket == commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_UNSPECIFIED {
				t.Fatalf("%s: record %d has UNSPECIFIED TrueBucket", name, i)
			}
			if rec.RecoveryProbability < 0 || rec.RecoveryProbability > 1 {
				t.Fatalf("%s: record %d has RecoveryProbability %v out of [0,1]", name, i, rec.RecoveryProbability)
			}
			if rec.ResponseDelaySeconds < 0 {
				t.Fatalf("%s: record %d has negative ResponseDelaySeconds", name, i)
			}
		}
	}
}

// ListScenarios is pure (no store, no queue, no producer), so it is
// unit-testable against a zero-value *Server, no infra needed.
func TestListScenariosRPCReturnsAllPresetsWithDescriptions(t *testing.T) {
	s := &Server{}
	resp, err := s.ListScenarios(context.Background(), &worldsimv1.ListScenariosRequest{})
	if err != nil {
		t.Fatalf("ListScenarios: %v", err)
	}
	if len(resp.GetScenarios()) != 4 {
		t.Fatalf("ListScenarios returned %d presets, want 4", len(resp.GetScenarios()))
	}
	names := map[string]bool{}
	for _, p := range resp.GetScenarios() {
		if p.GetDescription() == "" {
			t.Errorf("preset %q has no description", p.GetName())
		}
		names[p.GetName()] = true
	}
	for _, want := range []string{"normal", "bank-outage", "salary-day", "dead-cards"} {
		if !names[want] {
			t.Errorf("ListScenarios missing %q", want)
		}
	}
}
