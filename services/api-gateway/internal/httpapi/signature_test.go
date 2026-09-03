package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/thisizaro/Momotaro/internal/platform/webhooksig"
	ingestionv1 "github.com/thisizaro/Momotaro/proto/gen/ingestion/v1"
)

// webhookRoutes is every route verifyWebhookSignature guards, exercised
// table-driven below so the two routes cannot silently diverge on what
// "verified" means (docs/DECISIONS.md: this is the reason signature
// verification is shared middleware rather than duplicated per handler).
var webhookRoutes = []struct {
	name string
	path string
	body string
}{
	{
		name: "payment-failed",
		path: "/v1/webhooks/payment-failed",
		body: `{"type":"PAYMENT","amount_paise":1000,"failure_code":"BANK_TIMEOUT"}`,
	},
	{
		name: "payment-downtime",
		path: "/v1/webhooks/payment-downtime",
		body: razorpayDowntimeStartedPayload,
	},
}

func signedWebhookHandler() http.Handler {
	fakeIn := &fakeIngestion{eventResp: &ingestionv1.SubmitEventResponse{RecordId: "r1", BatchId: "b1"}}
	h := New(fakeIn, &fakeReporting{}, &fakeAudit{}, &fakeDecisionEngine{}, testAPIKey, 2*time.Second, 0, 0)
	h.SetWebhookSecrets(testWebhookSecret, "")
	return h.Routes()
}

func webhookRequest(path, body string, sigHeader *string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("X-API-Key", testAPIKey)
	req.Header.Set("Content-Type", "application/json")
	if sigHeader != nil {
		req.Header.Set(razorpaySignatureHeader, *sigHeader)
	}
	return req
}

// TestWebhookAcceptsAValidSignature is the green half of the red/green
// proof: a body signed with the Gateway's own configured secret is
// accepted, on both routes this middleware guards.
func TestWebhookAcceptsAValidSignature(t *testing.T) {
	for _, tc := range webhookRoutes {
		t.Run(tc.name, func(t *testing.T) {
			h := signedWebhookHandler()
			sig := webhooksig.Sign(testWebhookSecret, []byte(tc.body))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, webhookRequest(tc.path, tc.body, &sig))

			if rec.Code == http.StatusUnauthorized {
				t.Fatalf("status = 401 for a correctly signed body, want it to pass signature verification (body=%s)", rec.Body.String())
			}
		})
	}
}

// TestWebhookRejectsAnInvalidSignature: a syntactically plausible but wrong
// signature must be rejected, never accepted because it "looks like" a hex
// digest.
func TestWebhookRejectsAnInvalidSignature(t *testing.T) {
	for _, tc := range webhookRoutes {
		t.Run(tc.name, func(t *testing.T) {
			h := signedWebhookHandler()
			wrong := "0000000000000000000000000000000000000000000000000000000000000000"
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, webhookRequest(tc.path, tc.body, &wrong))

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401 for a wrong signature", rec.Code)
			}
		})
	}
}

// TestWebhookRejectsAMissingSignatureHeader: no X-Razorpay-Signature header
// at all must reject, not fall back to some other check.
func TestWebhookRejectsAMissingSignatureHeader(t *testing.T) {
	for _, tc := range webhookRoutes {
		t.Run(tc.name, func(t *testing.T) {
			h := signedWebhookHandler()
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, webhookRequest(tc.path, tc.body, nil))

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401 with no X-Razorpay-Signature header at all", rec.Code)
			}
		})
	}
}

// TestWebhookRejectsABodyAlteredAfterSigning is the exact bug this unit
// exists to prevent: verify-then-decode over the SAME bytes, never
// decode-then-reserialize. A signature computed over the original body must
// not validate a different body, even one differing by a single character.
func TestWebhookRejectsABodyAlteredAfterSigning(t *testing.T) {
	for _, tc := range webhookRoutes {
		t.Run(tc.name, func(t *testing.T) {
			h := signedWebhookHandler()
			sig := webhooksig.Sign(testWebhookSecret, []byte(tc.body))
			altered := tc.body + " "
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, webhookRequest(tc.path, altered, &sig))

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401 for a body altered after signing", rec.Code)
			}
		})
	}
}

// TestWebhookRejectsWhenNoSecretIsConfigured is the fail-closed proof: a
// Handler that never called SetWebhookSecrets (the zero value) must reject
// every webhook, including one carrying what would otherwise be a valid
// signature under some secret, rather than treating "no secret configured"
// as "skip verification".
func TestWebhookRejectsWhenNoSecretIsConfigured(t *testing.T) {
	for _, tc := range webhookRoutes {
		t.Run(tc.name, func(t *testing.T) {
			fakeIn := &fakeIngestion{eventResp: &ingestionv1.SubmitEventResponse{RecordId: "r1", BatchId: "b1"}}
			h := New(fakeIn, &fakeReporting{}, &fakeAudit{}, &fakeDecisionEngine{}, testAPIKey, 2*time.Second, 0, 0)
			// Deliberately never calling h.SetWebhookSecrets.
			sig := webhooksig.Sign("some-secret-an-attacker-might-guess", []byte(tc.body))
			rec := httptest.NewRecorder()
			h.Routes().ServeHTTP(rec, webhookRequest(tc.path, tc.body, &sig))

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401 with no webhook secret configured at all", rec.Code)
			}
		})
	}
}

// TestWebhookRotationAcceptsThePreviousSecret proves SetWebhookSecrets'
// second argument actually takes effect end to end: a body signed under the
// PREVIOUS secret still verifies while a rotation is in progress.
func TestWebhookRotationAcceptsThePreviousSecret(t *testing.T) {
	fakeIn := &fakeIngestion{eventResp: &ingestionv1.SubmitEventResponse{RecordId: "r1", BatchId: "b1"}}
	h := New(fakeIn, &fakeReporting{}, &fakeAudit{}, &fakeDecisionEngine{}, testAPIKey, 2*time.Second, 0, 0)
	h.SetWebhookSecrets("new-secret", "old-secret")

	body := `{"type":"PAYMENT","amount_paise":1000,"failure_code":"BANK_TIMEOUT"}`
	sig := webhooksig.Sign("old-secret", []byte(body))
	rec := httptest.NewRecorder()
	h.Routes().ServeHTTP(rec, webhookRequest("/v1/webhooks/payment-failed", body, &sig))

	if rec.Code == http.StatusUnauthorized {
		t.Fatalf("status = 401 for a body signed under the previous secret during rotation, want it accepted (body=%s)", rec.Body.String())
	}
}
