package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// GET /v1/help is documentation, not data, so it must answer with no
// X-API-Key at all: a client that does not already have a key needs a way
// to discover the routes that need one (docs/DEMO_READINESS.md Unit AK).
func TestHelpRequiresNoAuth(t *testing.T) {
	srv := httptest.NewServer(New(&fakeIngestion{}, &fakeReporting{}, &fakeAudit{}, nil, testAPIKey, 2*time.Second, 0, 0).Routes())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/help")
	if err != nil {
		t.Fatalf("GET /v1/help: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 with no X-API-Key set", resp.StatusCode)
	}
}

// The response must be the assembled contract, not a hand-maintained
// second copy that can drift from docs/API_GATEWAY.md: every route that
// document lists, in the shape a caller can act on.
func TestHelpListsEveryDocumentedRoute(t *testing.T) {
	srv := httptest.NewServer(New(&fakeIngestion{}, &fakeReporting{}, &fakeAudit{}, nil, testAPIKey, 2*time.Second, 0, 0).Routes())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/help")
	if err != nil {
		t.Fatalf("GET /v1/help: %v", err)
	}
	defer resp.Body.Close()

	var body struct {
		Routes []struct {
			Method      string `json:"method"`
			Path        string `json:"path"`
			Auth        string `json:"auth"`
			Description string `json:"description"`
		} `json:"routes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	want := []struct{ method, path string }{
		{"POST", "/v1/webhooks/payment-failed"},
		{"POST", "/v1/webhooks/payment-downtime"},
		{"POST", "/v1/batches"},
		{"GET", "/v1/batches"},
		{"GET", "/v1/batches/{batch_id}/report"},
		{"GET", "/v1/batches/{batch_id}/records"},
		{"GET", "/v1/records/{record_id}/audit"},
		{"GET", "/v1/batches/{batch_id}/invariants"},
		{"GET", "/v1/invariants"},
		{"WS", "/v1/batches/{batch_id}/live"},
		{"POST", "/v1/demo/batches"},
		{"GET", "/v1/demo/scenarios"},
		{"GET", "/v1/demo/world"},
		{"POST", "/v1/demo/inject-poison"},
	}
	for _, w := range want {
		found := false
		for _, r := range body.Routes {
			if r.Method == w.method && r.Path == w.path {
				found = true
				if r.Description == "" {
					t.Errorf("%s %s: empty description", w.method, w.path)
				}
				if r.Auth == "" {
					t.Errorf("%s %s: empty auth note", w.method, w.path)
				}
				break
			}
		}
		if !found {
			t.Errorf("missing from /v1/help: %s %s", w.method, w.path)
		}
	}
}

// Demo routes only exist on the real Gateway when DEMO_CONTROLS_ENABLED is
// set (Routes(), "structurally absent, not merely locked"). /help must say
// so rather than presenting them as unconditionally available, or the
// contract it assembles would be wrong for most deployments.
func TestHelpMarksDemoRoutesAsConditional(t *testing.T) {
	srv := httptest.NewServer(New(&fakeIngestion{}, &fakeReporting{}, &fakeAudit{}, nil, testAPIKey, 2*time.Second, 0, 0).Routes())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/help")
	if err != nil {
		t.Fatalf("GET /v1/help: %v", err)
	}
	defer resp.Body.Close()

	var body struct {
		Routes []struct {
			Path string `json:"path"`
			Auth string `json:"auth"`
		} `json:"routes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, r := range body.Routes {
		if strings.HasPrefix(r.Path, "/v1/demo/") {
			if !strings.Contains(r.Auth, "DEMO_CONTROLS_ENABLED") {
				t.Errorf("%s: auth note %q does not mention DEMO_CONTROLS_ENABLED", r.Path, r.Auth)
			}
		}
	}
}
