// demo.go implements the /v1/demo/* control surface (docs/API_GATEWAY.md,
// docs/PHASE5_5_IMPLEMENTATION.md Unit W). These routes exist only when
// DEMO_CONTROLS_ENABLED is true (services/api-gateway/cmd/main.go), which
// is what EnableDemoControls below gates: it is never called otherwise, so
// Routes never registers these handlers and a disabled deployment 404s
// rather than 401/403s on them -- the surface does not exist, it is not
// merely locked.
//
// Every handler here is a thin proxy onto worldsimv1.WorldSimulatorServiceClient,
// same as every other route in this package proxies onto Ingestion/Reporting/
// Audit: no business logic, no direct database access
// (docs/ARCHITECTURE.md section 3a). Batch seeding in particular is
// implemented on World Simulator, not here, because only a demo/ component
// may ever write GROUND_TRUTH (docs/ARCHITECTURE.md section 6); the
// Gateway never gains a database handle.
package httpapi

import (
	"context"
	"encoding/json"
	"net/http"

	worldsimv1 "github.com/thisizaro/Momotaro/proto/gen/worldsim/v1"
)

// EnableDemoControls registers worldsim as this Handler's backend for the
// /v1/demo/* routes. Call it before Routes(), and only when
// DEMO_CONTROLS_ENABLED is true: Routes() checks h.worldsim to decide
// whether to register these handlers at all.
func (h *Handler) EnableDemoControls(worldsim worldsimv1.WorldSimulatorServiceClient) {
	h.worldsim = worldsim
}

// Wire structs, hand-written per docs/API_GATEWAY.md wire convention 6: no
// protojson, no omitempty, every documented field always rendered.

type seedDemoBatchRequest struct {
	Scenario string `json:"scenario"`
	Count    int32  `json:"count"`
	Seed     int64  `json:"seed"`
}

type seedDemoBatchResponse struct {
	BatchID        string `json:"batch_id"`
	GeneratedCount int32  `json:"generated_count"`
	Seed           int64  `json:"seed"`
}

func (h *Handler) seedDemoBatch(w http.ResponseWriter, r *http.Request) {
	var req seedDemoBatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "malformed JSON body")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), h.callTimeout)
	defer cancel()

	resp, err := h.worldsim.SeedBatch(ctx, &worldsimv1.SeedBatchRequest{
		Scenario: req.Scenario,
		Count:    req.Count,
		Seed:     req.Seed,
	})
	if err != nil {
		writeGRPCError(w, err, "WORLD_SIMULATOR_UNAVAILABLE")
		return
	}
	writeJSON(w, http.StatusOK, seedDemoBatchResponse{
		BatchID:        resp.GetBatchId(),
		GeneratedCount: resp.GetGeneratedCount(),
		Seed:           resp.GetSeed(),
	})
}

type scenarioPresetJSON struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type listDemoScenariosResponse struct {
	Scenarios []scenarioPresetJSON `json:"scenarios"`
}

func (h *Handler) listDemoScenarios(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), h.callTimeout)
	defer cancel()

	resp, err := h.worldsim.ListScenarios(ctx, &worldsimv1.ListScenariosRequest{})
	if err != nil {
		writeGRPCError(w, err, "WORLD_SIMULATOR_UNAVAILABLE")
		return
	}

	scenarios := make([]scenarioPresetJSON, len(resp.GetScenarios()))
	for i, p := range resp.GetScenarios() {
		scenarios[i] = scenarioPresetJSON{Name: p.GetName(), Description: p.GetDescription()}
	}
	writeJSON(w, http.StatusOK, listDemoScenariosResponse{Scenarios: scenarios})
}

type pendingOutcomeJSON struct {
	RecordID      string `json:"record_id"`
	AttemptNumber int32  `json:"attempt_number"`
	Outcome       string `json:"outcome"`
	DueAt         string `json:"due_at"`
}

type getDemoWorldStateResponse struct {
	Pending []pendingOutcomeJSON `json:"pending"`
}

func (h *Handler) getDemoWorldState(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), h.callTimeout)
	defer cancel()

	resp, err := h.worldsim.GetWorldState(ctx, &worldsimv1.GetWorldStateRequest{})
	if err != nil {
		writeGRPCError(w, err, "WORLD_SIMULATOR_UNAVAILABLE")
		return
	}

	pending := make([]pendingOutcomeJSON, len(resp.GetPending()))
	for i, p := range resp.GetPending() {
		pending[i] = pendingOutcomeJSON{
			RecordID:      p.GetRecordId(),
			AttemptNumber: p.GetAttemptNumber(),
			Outcome:       p.GetOutcome().String(),
			DueAt:         formatTimestamp(p.GetDueAt()),
		}
	}
	writeJSON(w, http.StatusOK, getDemoWorldStateResponse{Pending: pending})
}

type injectDemoPoisonResponse struct {
	RecordID string `json:"record_id"`
	BatchID  string `json:"batch_id"`
}

func (h *Handler) injectDemoPoison(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), h.callTimeout)
	defer cancel()

	resp, err := h.worldsim.InjectPoison(ctx, &worldsimv1.InjectPoisonRequest{})
	if err != nil {
		writeGRPCError(w, err, "WORLD_SIMULATOR_UNAVAILABLE")
		return
	}
	writeJSON(w, http.StatusOK, injectDemoPoisonResponse{RecordID: resp.GetRecordId(), BatchID: resp.GetBatchId()})
}
