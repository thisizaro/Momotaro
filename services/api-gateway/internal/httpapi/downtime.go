// downtime.go implements POST /v1/webhooks/payment-downtime
// (docs/API_GATEWAY.md, docs/PHASE5_5_IMPLEMENTATION.md Unit Y): Razorpay's
// payment.downtime.started / .updated / .resolved webhooks, matched to
// Razorpay's real documented payload shape rather than a Gateway-invented
// flat body, exactly as promised in docs/API_GATEWAY.md.
//
// Signature verification (Unit Z) now runs before this handler is ever
// called: Routes() wraps it in verifyWebhookSignature (signature.go), which
// also owns the body-size cap (maxWebhookBodyBytes) that used to live here.
// Past that check every field below is still treated as untrusted input:
// nothing here can panic on a malformed payload (a decode failure is a 400,
// not a crash), and the raw body is never logged, at INFO or otherwise.
//
// Thin proxy onto decisionenginev1.DecisionEngineServiceClient, no business
// logic beyond translating Razorpay's nested wire shape into the RPC's flat
// one (docs/ARCHITECTURE.md section 3a): unix-seconds fields become
// time.Time only once they reach the Decision Engine's server.go, and the
// one piece of real translation work this layer does is
// downtimeInstrumentKey, extracting Razorpay's varying `instrument` object
// down to the single value the guardrail matches on.
package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"

	decisionenginev1 "github.com/thisizaro/Momotaro/proto/gen/decisionengine/v1"
)

// downtimeStatuses is the closed set entity.status must be one of. Unlike
// severity (an open string, forwarded whatever it is) this one decides how
// the Decision Engine writes the row (active vs resolved), so an
// unrecognised value is a 400 here rather than silently mis-filed there.
var downtimeStatuses = map[string]bool{"started": true, "updated": true, "resolved": true}

// downtimeWebhookRequest is Razorpay's real, documented event envelope
// (https://razorpay.com/docs/webhooks/payloads/payments/), matched exactly:
// this is NOT a Gateway-invented flat body the way an earlier draft of
// payment-failed was before docs/PHASE5_5_IMPLEMENTATION.md Unit Z's
// payload-shape work.
type downtimeWebhookRequest struct {
	Entity    string   `json:"entity"`
	AccountID string   `json:"account_id"`
	Event     string   `json:"event"`
	Contains  []string `json:"contains"`
	Payload   struct {
		PaymentDowntime struct {
			Entity downtimeWebhookEntity `json:"entity"`
		} `json:"payment.downtime"`
	} `json:"payload"`
	CreatedAt int64 `json:"created_at"`
}

// downtimeWebhookEntity is payload["payment.downtime"]["entity"]. Field
// notes that matter here, all from docs/PHASE5_5_IMPLEMENTATION.md Unit Y:
//   - Begin/End/CreatedAt/UpdatedAt are UNIX SECONDS, not milliseconds.
//   - End is a pointer: nil (JSON null) means the downtime is still
//     ongoing, never a zero-value non-nullable timestamp.
//   - Severity is treated as an open string, never validated against
//     "high"/"medium": those are Razorpay's documented examples, not a
//     closed list.
//   - Instrument varies by payment method (netbanking gives {"bank":...},
//     a card gives {"issuer":...,"type":...} or {"network":...,"type":...}),
//     so it is a plain map, never a fixed struct.
type downtimeWebhookEntity struct {
	ID               string            `json:"id"`
	Entity           string            `json:"entity"`
	Method           string            `json:"method"`
	Begin            int64             `json:"begin"`
	End              *int64            `json:"end"`
	Status           string            `json:"status"`
	Scheduled        bool              `json:"scheduled"`
	Severity         string            `json:"severity"`
	Instrument       map[string]string `json:"instrument"`
	InstrumentSchema []string          `json:"instrument_schema"`
	CreatedAt        int64             `json:"created_at"`
	UpdatedAt        int64             `json:"updated_at"`
}

type submitDowntimeEventResponse struct {
	DowntimeID string `json:"downtime_id"`
	Applied    bool   `json:"applied"`
}

// wellKnownInstrumentKeys is downtimeInstrumentKey's fallback when a payload
// carries no instrument_schema at all: the field names Razorpay's
// documented examples actually use, checked in a fixed order so the result
// is deterministic rather than picking whichever key Go's map iteration
// happens to visit first.
var wellKnownInstrumentKeys = []string{"bank", "issuer", "network", "vpa_handle", "wallet"}

// downtimeInstrumentKey extracts the single identifying value from
// Razorpay's `instrument` object, which the docs are explicit varies by
// payment method: netbanking gives {"bank":"VIJB"}, a card gives
// {"issuer":"SBIN","type":"credit"} or {"network":"MC","type":"credit"}.
// This never hardcodes one shape (docs/PHASE5_5_IMPLEMENTATION.md Unit Y:
// "instrument varies by payment method ... instrument_schema tells you its
// shape. Do not hardcode one shape"):
//
//  1. schema's own first entry names the identifying field. "type"
//     (credit/debit) is a qualifier alongside an issuer or network, never
//     itself a matchable identity, which is why only the FIRST schema entry
//     is used, not every key present.
//  2. Failing that (no schema, or the named field is missing), fall back to
//     a short list of well-known field names.
//  3. Failing that, the lexicographically first key present, so a payload
//     for a method this function has never seen still degrades to
//     *something* deterministic instead of matching nothing.
func downtimeInstrumentKey(instrument map[string]string, schema []string) string {
	if len(schema) > 0 {
		if v, ok := instrument[schema[0]]; ok && v != "" {
			return v
		}
	}
	for _, k := range wellKnownInstrumentKeys {
		if v, ok := instrument[k]; ok && v != "" {
			return v
		}
	}
	keys := make([]string, 0, len(instrument))
	for k := range instrument {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) > 0 {
		return instrument[keys[0]]
	}
	return ""
}

// submitDowntimeEvent handles Razorpay's payment-downtime webhook. It never
// waits on anything but the Decision Engine's own RPC (there is no
// asynchronous pipeline to hand this off to the way submitEvent hands a
// record to Ingestion), so unlike that route this responds 200, not 202.
func (h *Handler) submitDowntimeEvent(w http.ResponseWriter, r *http.Request) {
	var req downtimeWebhookRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "malformed JSON body")
		return
	}

	entity := req.Payload.PaymentDowntime.Entity
	if entity.ID == "" {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "payload.payment.downtime.entity.id is required")
		return
	}
	if entity.Method == "" {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "payload.payment.downtime.entity.method is required")
		return
	}
	if !downtimeStatuses[entity.Status] {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "payload.payment.downtime.entity.status must be one of started, updated, resolved")
		return
	}
	if entity.Begin <= 0 {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "payload.payment.downtime.entity.begin is required")
		return
	}

	grpcReq := &decisionenginev1.ReportDowntimeEventRequest{
		DowntimeId:    entity.ID,
		Method:        entity.Method,
		Status:        entity.Status,
		Scheduled:     entity.Scheduled,
		Severity:      entity.Severity,
		InstrumentKey: downtimeInstrumentKey(entity.Instrument, entity.InstrumentSchema),
		BeginUnix:     entity.Begin,
	}
	if entity.End != nil {
		grpcReq.HasEnd = true
		grpcReq.EndUnix = *entity.End
	}

	ctx, cancel := context.WithTimeout(r.Context(), h.callTimeout)
	defer cancel()

	resp, err := h.decisionEngine.ReportDowntimeEvent(ctx, grpcReq)
	if err != nil {
		writeError(w, http.StatusBadGateway, "DECISION_ENGINE_UNAVAILABLE", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, submitDowntimeEventResponse{DowntimeID: entity.ID, Applied: resp.GetApplied()})
}
