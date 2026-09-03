package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// sendResult is the outcome of one webhook POST. err, when non-nil, is
// always wrapped from the standard library's own request/transport errors
// (net/http, net/url); it is never built by formatting the API key or any
// other secret into a string, so it is always safe to log verbatim.
type sendResult struct {
	accepted bool
	status   int
	err      error
}

// sendEvent POSTs one payload to url (the full webhook endpoint) and
// classifies the result. Accepted means exactly "202 Accepted", the
// documented success response for POST /v1/webhooks/payment-failed
// (docs/API_GATEWAY.md); any other status, or a transport failure (gateway
// unreachable, timed out, connection refused), is not accepted.
func sendEvent(ctx context.Context, httpClient *http.Client, url, apiKey string, payload WebhookPayload) sendResult {
	body, err := json.Marshal(payload)
	if err != nil {
		return sendResult{err: fmt.Errorf("marshal payload: %w", err)}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return sendResult{err: fmt.Errorf("build request: %w", err)}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)

	resp, err := httpClient.Do(req)
	if err != nil {
		// http.Client's own error (a *url.Error wrapping the transport
		// cause) never includes request headers, so the API key cannot
		// leak through here.
		return sendResult{err: fmt.Errorf("request failed: %w", err)}
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	return sendResult{accepted: resp.StatusCode == http.StatusAccepted, status: resp.StatusCode}
}
