// signature.go verifies Razorpay's webhook signature
// (docs/API_GATEWAY.md "Webhook signature verification",
// docs/PHASE5_5_IMPLEMENTATION.md Unit Z), shared middleware for both
// POST /v1/webhooks/payment-failed and POST /v1/webhooks/payment-downtime
// rather than duplicated per handler (docs/DECISIONS.md): one place reads
// the body, one place verifies it, one place decides what "fail closed"
// means, so the two routes cannot silently diverge on any of that.
package httpapi

import (
	"bytes"
	"io"
	"net/http"

	"github.com/thisizaro/Momotaro/internal/platform/webhooksig"
)

// razorpaySignatureHeader is the header Razorpay signs every webhook with:
// an HMAC-SHA256 hex digest of the raw request body, keyed with the shared
// webhook secret (https://razorpay.com/docs/webhooks/validate-test/).
const razorpaySignatureHeader = "X-Razorpay-Signature"

// maxWebhookBodyBytes caps how much of a webhook request body this
// middleware will ever read, applied before anything else including
// signature verification itself, so an oversized or hostile payload cannot
// exhaust memory before any check gets a chance to reject it (the same rule
// Unit Y already applied to payment-downtime alone; this now covers
// payment-failed too, closing a gap that route had). Razorpay's real
// payloads are well under a kilobyte; this is generous headroom, not a
// tight fit.
const maxWebhookBodyBytes = 64 * 1024

// verifyWebhookSignature wraps a webhook route handler with Razorpay's
// signature check. Order is deliberate and is the whole point of this
// function:
//
//  1. Cap the body (http.MaxBytesReader).
//  2. Read the ENTIRE body into memory once, as raw bytes.
//  3. Verify the signature over those exact bytes, in constant time
//     (webhooksig.Verify, backed by hmac.Equal, never == or bytes.Equal on
//     the hex strings).
//  4. Only once verification succeeds, hand the request to the real
//     handler, with r.Body replaced by a fresh reader over the SAME bytes
//     just verified. The handler's own json.Decode therefore reads exactly
//     what was signed; nothing here decodes, mutates, or re-serializes the
//     body first, which is exactly what Razorpay's docs warn against ("Do
//     not parse or cast the webhook request body" before verifying).
//
// A missing header, a wrong signature, and no webhook secret configured at
// all are all indistinguishable to the caller: every one of them is a 401
// with the same generic message, never a hint at which part failed. This is
// also what makes an unset secret fail CLOSED rather than silently skip
// verification: webhooksig.Verify returns false for every signature when no
// secret is configured (h.webhookSecrets is nil unless SetWebhookSecrets
// was called), so there is no branch in this function that treats "no
// secret" as "let it through".
func (h *Handler) verifyWebhookSignature(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxWebhookBodyBytes)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "could not read request body")
			return
		}

		signature := r.Header.Get(razorpaySignatureHeader)
		if !webhooksig.Verify(h.webhookSecrets, body, signature) {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "webhook signature verification failed")
			return
		}

		r.Body = io.NopCloser(bytes.NewReader(body))
		next(w, r)
	}
}
