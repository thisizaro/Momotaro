package main

import (
	"math/rand"
	"reflect"
	"testing"

	"github.com/thisizaro/Momotaro/internal/platform/syntheticgen"
)

// structFieldNames lists v's exported field names, in declaration order.
func structFieldNames(v any) []string {
	t := reflect.TypeOf(v)
	names := make([]string, t.NumField())
	for i := range names {
		names[i] = t.Field(i).Name
	}
	return names
}

var validWireTypes = map[string]bool{
	"PAYMENT":  true,
	"MANDATE":  true,
	"CHECKOUT": true,
	"INVOICE":  true,
}

func TestBuildPayloadProducesValidWireShape(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	pool := syntheticgen.InstrumentRefPool(100)

	for i := 0; i < 500; i++ {
		p, err := buildPayload(rng, pool)
		if err != nil {
			t.Fatalf("record %d: buildPayload: %v", i, err)
		}
		if !validWireTypes[p.Type] {
			t.Fatalf("record %d: Type = %q, want one of PAYMENT/MANDATE/CHECKOUT/INVOICE (docs/API_GATEWAY.md wire spelling)", i, p.Type)
		}
		if p.AmountPaise <= 0 {
			t.Fatalf("record %d: AmountPaise = %d, want positive", i, p.AmountPaise)
		}
		if p.Currency != "INR" {
			t.Fatalf("record %d: Currency = %q, want INR", i, p.Currency)
		}
		if p.FailureCode == "" {
			t.Fatalf("record %d: FailureCode is empty, but submitEvent requires it", i)
		}
	}
}

// TestBuildPayloadHasRealisticVariety proves the CLI draws from the same
// vocabulary the real system uses (syntheticgen), not a fixed handful of
// hand-picked values: over enough draws, more than one type and more than
// one failure code must appear.
func TestBuildPayloadHasRealisticVariety(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	pool := syntheticgen.InstrumentRefPool(100)

	types := map[string]bool{}
	codes := map[string]bool{}
	amounts := map[int64]bool{}
	for i := 0; i < 300; i++ {
		p, err := buildPayload(rng, pool)
		if err != nil {
			t.Fatalf("buildPayload: %v", err)
		}
		types[p.Type] = true
		codes[p.FailureCode] = true
		amounts[p.AmountPaise] = true
	}
	if len(types) < 2 {
		t.Errorf("only %d distinct record type(s) across 300 draws, want a real spread", len(types))
	}
	if len(codes) < 5 {
		t.Errorf("only %d distinct failure code(s) across 300 draws, want a real spread", len(codes))
	}
	if len(amounts) < 50 {
		t.Errorf("only %d distinct amount(s) across 300 draws, want a real spread", len(amounts))
	}
}

// TestBuildPayloadNeverCarriesGroundTruth documents, as a running test
// rather than only a comment, that the webhook wire shape this CLI sends
// has no field for anything syntheticgen.GeneratedRecord knows about the
// hidden answer key (TrueBucket, RecoveryProbability,
// WrongActionProbability, ResponseDelaySeconds). WebhookPayload's own
// field list is the enforcement; this test is a guard against someone
// widening it later without noticing what that would mean
// (docs/ARCHITECTURE.md section 6: only scripts/batchgen may ever write
// GROUND_TRUTH, and the webhook route must have no honest way to carry it).
func TestBuildPayloadNeverCarriesGroundTruth(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	pool := syntheticgen.InstrumentRefPool(10)
	p, err := buildPayload(rng, pool)
	if err != nil {
		t.Fatalf("buildPayload: %v", err)
	}
	// Reflection over an allow-list of exactly the fields the frozen
	// webhook contract documents (docs/API_GATEWAY.md).
	wantFields := map[string]bool{
		"Type": true, "AmountPaise": true, "Currency": true,
		"FailureCode": true, "InstrumentRef": true,
	}
	fields := structFieldNames(p)
	if len(fields) != len(wantFields) {
		t.Fatalf("WebhookPayload has fields %v, want exactly %v", fields, wantFields)
	}
	for _, f := range fields {
		if !wantFields[f] {
			t.Errorf("WebhookPayload has unexpected field %q; the webhook route has no honest way to carry ground truth", f)
		}
	}
}
