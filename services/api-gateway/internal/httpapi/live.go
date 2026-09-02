package httpapi

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	reportingv1 "github.com/thisizaro/Momotaro/proto/gen/reporting/v1"
)

// Wire shape for one relayed message, mirroring reporting.v1.BatchUpdate
// exactly (docs/API_GATEWAY.md). recovered_delta_paise is always present,
// even as 0, so the dashboard can add it to a running total unconditionally.
type batchUpdateJSON struct {
	RecordID            string `json:"record_id"`
	FromState           string `json:"from_state"`
	ToState             string `json:"to_state"`
	Ts                  string `json:"ts"`
	RecoveredDeltaPaise int64  `json:"recovered_delta_paise"`
}

func toBatchUpdateJSON(u *reportingv1.BatchUpdate) batchUpdateJSON {
	return batchUpdateJSON{
		RecordID:            u.GetRecordId(),
		FromState:           u.GetFromState().String(),
		ToState:             u.GetToState().String(),
		Ts:                  formatTimestamp(u.GetTs()),
		RecoveredDeltaPaise: u.GetRecoveredDeltaPaise(),
	}
}

// liveUpdates relays Reporting's StreamBatchUpdates gRPC stream onto a
// browser WebSocket verbatim: the browser never knows gRPC or Kafka exist
// (docs/API_GATEWAY.md). This route sits outside withAuth (Routes() in
// handler.go) because a browser's WS handshake cannot set X-API-Key; the
// key is checked as the negotiated Sec-WebSocket-Protocol instead, closing
// gap 5.
//
// websocket.Accept refuses a cross-origin handshake by default (that is
// the library's own behaviour, nothing this codebase opted into), which is
// why the dashboard's Vite dev server on :5173 got a 403 against the
// Gateway on :8090 in every dev/demo run: they are different origins.
// h.wsAllowedOrigins (set via SetWSAllowedOrigins, from WS_ALLOWED_ORIGINS)
// extends that allowlist without weakening it: nil keeps same-origin-only,
// exactly today's behaviour.
func (h *Handler) liveUpdates(w http.ResponseWriter, r *http.Request) {
	batchID := r.PathValue("batch_id")
	if batchID == "" {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "batch_id is required")
		return
	}
	if !offersSubprotocol(r, h.apiKey) {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing or invalid Sec-WebSocket-Protocol")
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		Subprotocols:   []string{h.apiKey},
		OriginPatterns: h.wsAllowedOrigins,
	})
	if err != nil {
		return
	}
	defer conn.CloseNow()

	ctx := r.Context()
	stream, err := h.reporting.StreamBatchUpdates(ctx, &reportingv1.StreamBatchUpdatesRequest{BatchId: batchID})
	if err != nil {
		conn.Close(websocket.StatusInternalError, "reporting unavailable")
		return
	}

	for {
		resp, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				conn.Close(websocket.StatusNormalClosure, "")
			} else {
				conn.Close(websocket.StatusInternalError, "upstream stream error")
			}
			return
		}
		if err := wsjson.Write(ctx, conn, toBatchUpdateJSON(resp.GetUpdate())); err != nil {
			return
		}
	}
}

// offersSubprotocol reports whether want is one of the WebSocket
// subprotocols the client offered in Sec-WebSocket-Protocol. An empty want
// (no API key configured) never matches, matching withAuth's own refusal
// of an empty apiKey.
func offersSubprotocol(r *http.Request, want string) bool {
	if want == "" {
		return false
	}
	for _, header := range r.Header.Values("Sec-WebSocket-Protocol") {
		for _, p := range strings.Split(header, ",") {
			if strings.TrimSpace(p) == want {
				return true
			}
		}
	}
	return false
}
