package httpapi

import "net/http"

// helpRoute is one row of the assembled contract GET /v1/help answers with.
// The fields exist to be read by a caller deciding what to call next, not
// to be pretty: which HTTP verb, which path, what it needs in the
// Authorization sense, and one sentence of what it does.
type helpRoute struct {
	Method      string `json:"method"`
	Path        string `json:"path"`
	Auth        string `json:"auth"`
	Description string `json:"description"`
}

type helpResponse struct {
	Routes []helpRoute `json:"routes"`
}

// authAPIKey and authWebhook are the two auth shapes every non-demo route
// on this Gateway actually uses (docs/API_GATEWAY.md "Wire conventions"
// and "Webhook signature verification"). Named once so the eleven routes
// below stay consistent instead of each writing its own phrasing.
const (
	authAPIKey  = "X-API-Key header"
	authWebhook = "X-API-Key header, plus X-Razorpay-Signature (HMAC-SHA256 over the raw body)"
	authDemo    = "X-API-Key header; route only exists when DEMO_CONTROLS_ENABLED is set on this deployment (docs/PHASE5_5_IMPLEMENTATION.md Unit W), 404 otherwise"
	authNone    = "none; this route exists to be discoverable without a key already in hand"
	authWS      = "API key as the WebSocket subprotocol, not a header (a WS handshake cannot set one): new WebSocket(url, [apiKey])"
)

// helpRoutes is the contract docs/API_GATEWAY.md documents, assembled by
// hand rather than reflected off Routes(), because the wire shape a caller
// needs (method, path, auth, one sentence) is not the same thing as the Go
// routing table, and reflecting the mux would describe the code, not the
// contract. Keep this in the same order as that document's headings, and
// add a row here in the same PR that adds a route there.
var helpRoutes = []helpRoute{
	{"GET", "/v1/help", authNone, "This list."},
	{"POST", "/v1/webhooks/payment-failed", authWebhook, "The production entry point: one payment failure event, as it happens."},
	{"POST", "/v1/webhooks/payment-downtime", authWebhook, "Razorpay's payment.downtime.started / .updated / .resolved webhooks, consumed as a retry guardrail."},
	{"POST", "/v1/batches", authAPIKey, "Submit a batch of records for the agent to process."},
	{"GET", "/v1/batches", authAPIKey, "List batches, most recent first."},
	{"GET", "/v1/batches/{batch_id}/report", authAPIKey, "The headline numbers for one batch: recovery rate, spend, accuracy against ground truth if it has any."},
	{"GET", "/v1/batches/{batch_id}/records", authAPIKey, "Paginated record list for one batch. Powers the dashboard's record table."},
	{"GET", "/v1/records/{record_id}/audit", authAPIKey, "Full, ordered audit trail for one record, including the decision_trace that shows the alternatives the agent priced and rejected."},
	{"GET", "/v1/batches/{batch_id}/invariants", authAPIKey, "System invariant check scoped to one batch: stopping-rule violations, incomplete audit trails, impossible transitions."},
	{"GET", "/v1/invariants", authAPIKey, "The same invariant check across every batch."},
	{"WS", "/v1/batches/{batch_id}/live", authWS, "Streams incremental updates as a batch's records change state."},
	{"POST", "/v1/demo/batches", authDemo, "Seed a batch of synthetic records with a sealed, hidden ground truth."},
	{"GET", "/v1/demo/scenarios", authDemo, "List the demo scenario presets and what each one is built to show."},
	{"GET", "/v1/demo/world", authDemo, "The World Simulator's live state: every delayed outcome still queued and when it is due."},
	{"POST", "/v1/demo/inject-poison", authDemo, "Publish one raw.events message for a record id that was never inserted, to demonstrate the dead-letter path live."},
	{"GET", "/v1/demo/config", authDemo, "Read-only: the guardrail and LLM-routing values the agent is bounded by, fixed at process startup."},
}

// help answers GET /v1/help. Unauthenticated on purpose: a caller who does
// not already have an API key needs a way to discover which routes need
// one (docs/DEMO_READINESS.md Unit AK).
//
// Content negotiated: a browser's Accept header prefers text/html, so it
// gets a rendered page (help_page.go), the human-readable version the user
// actually asked for, "a help doc for someone trying to connect to the
// real system". Everything else, including curl's default Accept: */*,
// keeps getting the JSON below, which is the documented contract and what
// the tests above assert.
func (h *Handler) help(w http.ResponseWriter, r *http.Request) {
	if wantsHelpHTML(r) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write(helpHTMLOnce())
		return
	}
	writeJSON(w, http.StatusOK, helpResponse{Routes: helpRoutes})
}
