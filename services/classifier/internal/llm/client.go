package llm

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"
)

// maxResponseBytes bounds what this service will read from a provider. A
// model API is a third party: a stuck or hostile endpoint streaming megabytes
// should cost a bounded amount of memory, not an OOM in a pod whose real job
// is classifying payments.
const maxResponseBytes = 1 << 20 // 1 MiB

// RateLimitedError is a 429 from a provider, kept distinct from every other
// failure on purpose.
//
// On the free tiers this project runs on (30 RPM Groq, 10 to 15 RPM Gemini),
// rate limiting is the failure most likely to actually fire, and it means
// something different from an outage: the provider is healthy and will serve
// us again at a known time. Phase 3 Unit D's circuit breaker opens immediately
// on this rather than after N consecutive failures, and uses RetryAfter as the
// cooldown when the provider supplied one (docs/DECISIONS.md 2026-08-28).
// Detecting it belongs here, in the only code that sees an HTTP status; acting
// on it belongs to the breaker.
type RateLimitedError struct {
	Provider string
	// RetryAfter is zero when the provider sent no usable Retry-After header.
	RetryAfter time.Duration
}

func (e *RateLimitedError) Error() string {
	if e.RetryAfter > 0 {
		return fmt.Sprintf("%s rate limited, retry after %s", e.Provider, e.RetryAfter)
	}
	return fmt.Sprintf("%s rate limited, no retry-after supplied", e.Provider)
}

// StatusError is any other non-2xx response.
type StatusError struct {
	Provider string
	Code     int
	Body     string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("%s returned HTTP %d: %s", e.Provider, e.Code, truncate(e.Body, 200))
}

// client is the shared transport. Both vendors use it; neither owns one.
type client struct {
	http *http.Client
}

func newClient() *client {
	// No Timeout on the http.Client itself: the deadline comes from the
	// context the provider chain hands down (provider/budget.go), and a second
	// independent timeout here would silently win or lose depending on which
	// number someone edited last. Transport-level limits are different, they
	// bound connection setup rather than the call.
	return &client{http: &http.Client{
		Transport: &http.Transport{
			DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: 0, // governed by ctx
			MaxIdleConnsPerHost:   4,
			IdleConnTimeout:       90 * time.Second,
		},
	}}
}

// do sends the request and returns the body, converting HTTP status into the
// typed errors above so callers never inspect a status code themselves.
func (c *client) do(providerName string, req *http.Request) ([]byte, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		// Includes context deadline and cancellation, which the chain maps to
		// a timeout hop (provider/chain.go hopResultForError).
		return nil, fmt.Errorf("%s request: %w", providerName, err)
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if readErr != nil {
		return nil, fmt.Errorf("%s read response: %w", providerName, readErr)
	}

	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, &RateLimitedError{Provider: providerName, RetryAfter: retryAfter(resp.Header)}
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return nil, &StatusError{Provider: providerName, Code: resp.StatusCode, Body: string(body)}
	}
	return body, nil
}

// retryAfter reads the header in both forms RFC 9110 allows: delay-seconds,
// and an HTTP-date. Returns zero when absent or unparseable, which the caller
// treats as "fall back to the configured cooldown" rather than as an error.
func retryAfter(h http.Header) time.Duration {
	v := h.Get("Retry-After")
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs < 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
