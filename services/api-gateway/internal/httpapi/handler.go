// Package httpapi implements the API Gateway's external HTTP contract
// (docs/API_GATEWAY.md). It translates HTTP in to gRPC out, and does
// nothing else: no business logic, no direct database or Kafka access
// (docs/ARCHITECTURE.md section 3a).
package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	auditv1 "github.com/thisizaro/Momotaro/proto/gen/audit/v1"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	decisionenginev1 "github.com/thisizaro/Momotaro/proto/gen/decisionengine/v1"
	ingestionv1 "github.com/thisizaro/Momotaro/proto/gen/ingestion/v1"
	reportingv1 "github.com/thisizaro/Momotaro/proto/gen/reporting/v1"
	worldsimv1 "github.com/thisizaro/Momotaro/proto/gen/worldsim/v1"
	"golang.org/x/time/rate"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Handler serves the Gateway's HTTP routes.
type Handler struct {
	ingestion      ingestionv1.IngestionServiceClient
	reporting      reportingv1.ReportingServiceClient
	audit          auditv1.AuditServiceClient
	decisionEngine decisionenginev1.DecisionEngineServiceClient
	apiKey         string
	callTimeout    time.Duration
	limiter        *rate.Limiter // nil means rate limiting is disabled

	// worldsim backs /v1/demo/* (demo.go). nil unless EnableDemoControls was
	// called, which cmd/main.go only does when DEMO_CONTROLS_ENABLED is
	// true. Routes() uses this nil-ness to decide whether those routes
	// exist at all (docs/PHASE5_5_IMPLEMENTATION.md Unit W: a disabled
	// deployment must 404 on them, not merely refuse them).
	worldsim worldsimv1.WorldSimulatorServiceClient

	// wsAllowedOrigins lists extra origins the live WebSocket route
	// (live.go) accepts beyond same-origin, set via SetWSAllowedOrigins.
	// Nil (the zero value, and the default with WS_ALLOWED_ORIGINS unset)
	// means only same-origin requests are accepted, exactly
	// coder/websocket's own default, so an operator who does nothing gets
	// today's behaviour, not a permissive Gateway.
	wsAllowedOrigins []string
}

// SetWSAllowedOrigins configures which cross-origin WebSocket handshakes
// liveUpdates accepts, in addition to same-origin ones which are always
// allowed. Call it before Routes(). Passing nil or an empty slice restores
// the default: same-origin only, matching the API Gateway's behaviour
// before WS_ALLOWED_ORIGINS existed.
//
// Each entry is a host pattern for github.com/coder/websocket's
// AcceptOptions.OriginPatterns: matched case-insensitively with path.Match,
// against the request Origin's host, or against "scheme://host" if the
// pattern itself contains "://". cmd/main.go sets this from
// WS_ALLOWED_ORIGINS, a comma-separated list, e.g.
// "http://localhost:5173" for the dashboard's Vite dev server.
func (h *Handler) SetWSAllowedOrigins(origins []string) {
	h.wsAllowedOrigins = origins
}

// New returns a Handler. apiKey is the static shared key every request must
// present in X-API-Key (docs/ARCHITECTURE.md section 17: deliberately not
// real user auth, so a judge can try the API with zero setup).
//
// decisionEngine backs POST /v1/webhooks/payment-downtime
// (docs/PHASE5_5_IMPLEMENTATION.md Unit Y), a production route like
// payment-failed, not one gated behind demo controls, so unlike
// EnableDemoControls's worldsim client this is a required constructor
// argument.
//
// rateLimitRPS/rateLimitBurst configure a single token bucket shared across
// every request the Gateway receives (there is no per-caller identity to key
// on, section 17 again: this system has no concept of a "user"). Either
// value <= 0 disables rate limiting entirely, useful for tests and for local
// development against a single caller.
func New(ingestion ingestionv1.IngestionServiceClient, reporting reportingv1.ReportingServiceClient, audit auditv1.AuditServiceClient, decisionEngine decisionenginev1.DecisionEngineServiceClient, apiKey string, callTimeout time.Duration, rateLimitRPS float64, rateLimitBurst int) *Handler {
	h := &Handler{ingestion: ingestion, reporting: reporting, audit: audit, decisionEngine: decisionEngine, apiKey: apiKey, callTimeout: callTimeout}
	if rateLimitRPS > 0 && rateLimitBurst > 0 {
		h.limiter = rate.NewLimiter(rate.Limit(rateLimitRPS), rateLimitBurst)
	}
	return h
}

// Routes returns the Gateway's HTTP handler. Rate limiting applies first, so
// an over-limit caller cannot even reach the auth check, then auth, on every
// route except one.
//
// WS /v1/batches/{batch_id}/live is deliberately outside withAuth: a
// browser's WebSocket handshake cannot set a custom header, so X-API-Key
// does not apply there (docs/API_GATEWAY.md gap 5). That route checks the
// negotiated WebSocket subprotocol itself, inside liveUpdates.
func (h *Handler) Routes() http.Handler {
	authenticated := http.NewServeMux()
	authenticated.HandleFunc("POST /v1/batches", h.submitBatch)
	authenticated.HandleFunc("POST /v1/webhooks/payment-failed", h.submitEvent)
	authenticated.HandleFunc("POST /v1/webhooks/payment-downtime", h.submitDowntimeEvent)
	authenticated.HandleFunc("GET /v1/batches", h.listBatches)
	authenticated.HandleFunc("GET /v1/batches/{batch_id}/report", h.getBatchReport)
	authenticated.HandleFunc("GET /v1/batches/{batch_id}/records", h.listBatchRecords)
	authenticated.HandleFunc("GET /v1/records/{record_id}/audit", h.getRecordAudit)
	authenticated.HandleFunc("GET /v1/batches/{batch_id}/invariants", h.verifyInvariantsForBatch)
	authenticated.HandleFunc("GET /v1/invariants", h.verifyInvariantsSystemWide)

	// /v1/demo/* only exists when EnableDemoControls was called
	// (cmd/main.go, gated on DEMO_CONTROLS_ENABLED). Registering it
	// conditionally, rather than always registering and checking the flag
	// inside each handler, is what makes a disabled deployment 404 on these
	// routes instead of 403: the surface is structurally absent, not merely
	// locked (docs/PHASE5_5_IMPLEMENTATION.md Unit W).
	if h.worldsim != nil {
		authenticated.HandleFunc("POST /v1/demo/batches", h.seedDemoBatch)
		authenticated.HandleFunc("GET /v1/demo/scenarios", h.listDemoScenarios)
		authenticated.HandleFunc("GET /v1/demo/world", h.getDemoWorldState)
		authenticated.HandleFunc("POST /v1/demo/inject-poison", h.injectDemoPoison)
	}

	mux := http.NewServeMux()
	mux.Handle("/", h.withAuth(authenticated))
	mux.HandleFunc("GET /v1/batches/{batch_id}/live", h.liveUpdates)

	return h.withCORS(h.withRateLimit(mux))
}

// withCORS lets a browser call this API from a different origin, e.g. the
// dashboard's Vite dev server on :5173 against the Gateway on :8090. This is
// the same hackathon simplification as the API key itself (docs/ARCHITECTURE.md
// section 17): allow any origin rather than an allowlist, since there is no
// session or cookie to protect and the static key is the only credential.
//
// A CORS preflight (OPTIONS) request never carries X-API-Key -- browsers
// refuse to put custom headers on a preflight -- so it must be answered here,
// outside withAuth and before it, or every browser call would 401 on its
// preflight before ever reaching the real handler.
func (h *Handler) withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "X-API-Key, Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) withRateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.limiter != nil && !h.limiter.Allow() {
			writeError(w, http.StatusTooManyRequests, "RATE_LIMITED", "too many requests")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != h.apiKey || h.apiKey == "" {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing or invalid X-API-Key header")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// submitBatchRequestRecord is the wire shape for one record in a batch
// submission. type uses short names (PAYMENT, MANDATE, CHECKOUT, INVOICE)
// rather than the internal proto enum spelling, since this is the one
// contract external callers (the demo load generator, a judge with curl)
// actually see.
type submitBatchRequestRecord struct {
	Type          string `json:"type"`
	AmountPaise   int64  `json:"amount_paise"`
	Currency      string `json:"currency"`
	FailureCode   string `json:"failure_code"`
	InstrumentRef string `json:"instrument_ref"`
}

type submitBatchRequest struct {
	Source  string                     `json:"source"`
	Records []submitBatchRequestRecord `json:"records"`
}

type submitBatchResponse struct {
	BatchID       string            `json:"batch_id"`
	AcceptedCount int32             `json:"accepted_count"`
	Rejected      map[string]string `json:"rejected"`
}

type errorResponse struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// recordTypeNames maps the Gateway's external record type spelling to the
// internal proto enum. Unrecognised or missing values fall through to
// RECORD_TYPE_UNSPECIFIED, which Ingestion's own validation rejects with a
// clear reason rather than the Gateway silently guessing.
var recordTypeNames = map[string]commonv1.RecordType{
	"PAYMENT":  commonv1.RecordType_RECORD_TYPE_PAYMENT,
	"MANDATE":  commonv1.RecordType_RECORD_TYPE_MANDATE,
	"CHECKOUT": commonv1.RecordType_RECORD_TYPE_CHECKOUT,
	"INVOICE":  commonv1.RecordType_RECORD_TYPE_INVOICE,
}

func (h *Handler) submitBatch(w http.ResponseWriter, r *http.Request) {
	var req submitBatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "malformed JSON body")
		return
	}
	if len(req.Records) == 0 {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "records must not be empty")
		return
	}

	records := make([]*ingestionv1.NewRecord, len(req.Records))
	for i, rec := range req.Records {
		records[i] = &ingestionv1.NewRecord{
			Type:          recordTypeNames[rec.Type],
			AmountPaise:   rec.AmountPaise,
			Currency:      rec.Currency,
			FailureCode:   rec.FailureCode,
			InstrumentRef: rec.InstrumentRef,
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), h.callTimeout)
	defer cancel()

	resp, err := h.ingestion.SubmitBatch(ctx, &ingestionv1.SubmitBatchRequest{Source: req.Source, Records: records})
	if err != nil {
		writeError(w, http.StatusBadGateway, "INGESTION_UNAVAILABLE", err.Error())
		return
	}

	rejected := make(map[string]string, len(resp.GetRejected()))
	for idx, reason := range resp.GetRejected() {
		rejected[strconv.Itoa(int(idx))] = reason
	}

	writeJSON(w, http.StatusOK, submitBatchResponse{
		BatchID:       resp.GetBatchId(),
		AcceptedCount: resp.GetAcceptedCount(),
		Rejected:      rejected,
	})
}

// submitEventRequest is the wire shape for the production webhook entry
// point: one failure event, as it happens (docs/ARCHITECTURE.md section 0a).
// occurred_at is optional RFC3339; left unset it defaults to receipt time,
// same as the underlying NewRecord field.
type submitEventRequest struct {
	Type           string `json:"type"`
	AmountPaise    int64  `json:"amount_paise"`
	Currency       string `json:"currency"`
	FailureCode    string `json:"failure_code"`
	InstrumentRef  string `json:"instrument_ref"`
	OccurredAt     string `json:"occurred_at"`
	IdempotencyKey string `json:"idempotency_key"`
}

type submitEventResponse struct {
	RecordID     string `json:"record_id"`
	BatchID      string `json:"batch_id"`
	Deduplicated bool   `json:"deduplicated"`
}

// submitEvent handles the production entry point. A webhook sender must
// never be made to wait on the recovery pipeline, so this returns as soon as
// Ingestion has durably accepted the record (docs/API_GATEWAY.md).
func (h *Handler) submitEvent(w http.ResponseWriter, r *http.Request) {
	var req submitEventRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "malformed JSON body")
		return
	}
	if req.FailureCode == "" {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "failure_code is required")
		return
	}

	record := &ingestionv1.NewRecord{
		Type:          recordTypeNames[req.Type],
		AmountPaise:   req.AmountPaise,
		Currency:      req.Currency,
		FailureCode:   req.FailureCode,
		InstrumentRef: req.InstrumentRef,
	}
	if req.OccurredAt != "" {
		if ts, err := time.Parse(time.RFC3339, req.OccurredAt); err == nil {
			record.OccurredAt = timestamppb.New(ts)
		} else {
			writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "occurred_at must be RFC3339")
			return
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), h.callTimeout)
	defer cancel()

	resp, err := h.ingestion.SubmitEvent(ctx, &ingestionv1.SubmitEventRequest{
		Record:         record,
		IdempotencyKey: req.IdempotencyKey,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, "INGESTION_UNAVAILABLE", err.Error())
		return
	}

	// 202: accepted for asynchronous processing, never made to wait on the
	// pipeline (docs/API_GATEWAY.md).
	writeJSON(w, http.StatusAccepted, submitEventResponse{
		RecordID:     resp.GetRecordId(),
		BatchID:      resp.GetBatchId(),
		Deduplicated: resp.GetDeduplicated(),
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, errCode, message string) {
	writeJSON(w, code, errorResponse{Error: errorBody{Code: errCode, Message: message}})
}
