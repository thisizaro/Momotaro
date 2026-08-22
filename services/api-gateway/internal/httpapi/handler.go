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

	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	ingestionv1 "github.com/thisizaro/Momotaro/proto/gen/ingestion/v1"
)

// Handler serves the Gateway's HTTP routes.
type Handler struct {
	ingestion   ingestionv1.IngestionServiceClient
	apiKey      string
	callTimeout time.Duration
}

// New returns a Handler. apiKey is the static shared key every request must
// present in X-API-Key (docs/ARCHITECTURE.md section 17: deliberately not
// real user auth, so a judge can try the API with zero setup).
func New(ingestion ingestionv1.IngestionServiceClient, apiKey string, callTimeout time.Duration) *Handler {
	return &Handler{ingestion: ingestion, apiKey: apiKey, callTimeout: callTimeout}
}

// Routes returns the Gateway's HTTP handler, auth applied to every route.
func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/batches", h.submitBatch)
	return h.withAuth(mux)
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
	Rejected      map[string]string `json:"rejected,omitempty"`
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

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, errCode, message string) {
	writeJSON(w, code, errorResponse{Error: errorBody{Code: errCode, Message: message}})
}
