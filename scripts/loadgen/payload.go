package main

import (
	"fmt"
	"math/rand"

	"github.com/thisizaro/Momotaro/internal/platform/syntheticgen"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

// WebhookPayload is the wire shape POST /v1/webhooks/payment-failed expects
// (docs/API_GATEWAY.md). occurred_at and idempotency_key are both optional
// on the wire and deliberately omitted here: occurred_at defaults to
// receipt time, which is the honest timestamp for traffic actually arriving
// now, and idempotency_key exists for a real webhook sender's at-least-once
// redelivery, which this generator has no need to simulate.
//
// Fields are exactly the four the frozen contract documents for this route,
// nothing more: in particular no TrueBucket/RecoveryProbability/
// WrongActionProbability/ResponseDelaySeconds, the hidden ground-truth
// fields syntheticgen.GeneratedRecord also carries. The webhook route has
// no field for them, by design (only scripts/batchgen, writing straight to
// Postgres, may ever seed GROUND_TRUTH, docs/ARCHITECTURE.md section 6),
// and this struct's shape is what keeps that true here even if someone
// later reaches for a convenient extra field.
type WebhookPayload struct {
	Type          string `json:"type"`
	AmountPaise   int64  `json:"amount_paise"`
	Currency      string `json:"currency"`
	FailureCode   string `json:"failure_code"`
	InstrumentRef string `json:"instrument_ref"`
}

// recordTypeWireNames mirrors services/api-gateway/internal/httpapi/
// handler.go's recordTypeNames map, in reverse: the Gateway's own short
// spelling for this one field (PAYMENT/MANDATE/CHECKOUT/INVOICE), distinct
// from the internal proto enum name. Not imported from there: that map is
// unexported inside api-gateway's internal/ package, which nothing outside
// services/api-gateway may import (AGENTS.md's "cross-service import is a
// compile error" rule), so this is the one small piece of wire knowledge
// scripts/loadgen keeps for itself.
var recordTypeWireNames = map[commonv1.RecordType]string{
	commonv1.RecordType_RECORD_TYPE_PAYMENT:  "PAYMENT",
	commonv1.RecordType_RECORD_TYPE_MANDATE:  "MANDATE",
	commonv1.RecordType_RECORD_TYPE_CHECKOUT: "CHECKOUT",
	commonv1.RecordType_RECORD_TYPE_INVOICE:  "INVOICE",
}

// buildPayload draws one record from syntheticgen, the same generation
// logic scripts/batchgen and the World Simulator use, so live traffic looks
// like a real revenue-at-risk feed rather than a second, invented
// vocabulary. Only the four public wire fields are kept; see WebhookPayload.
func buildPayload(rng *rand.Rand, instrumentPool []string) (WebhookPayload, error) {
	rec := syntheticgen.GenerateRecord(rng)
	wireType, ok := recordTypeWireNames[rec.Type]
	if !ok {
		return WebhookPayload{}, fmt.Errorf("no wire name for record type %v", rec.Type)
	}
	return WebhookPayload{
		Type:          wireType,
		AmountPaise:   rec.AmountPaise,
		Currency:      "INR",
		FailureCode:   rec.FailureCode,
		InstrumentRef: syntheticgen.PickInstrumentRef(rng, rec.Type, instrumentPool),
	}, nil
}
