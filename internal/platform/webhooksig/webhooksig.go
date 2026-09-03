// Package webhooksig implements Razorpay's webhook signature scheme: an
// HMAC-SHA256 hex digest of the raw request body, keyed with a shared
// webhook secret, carried in the X-Razorpay-Signature header
// (https://razorpay.com/docs/webhooks/validate-test/). Razorpay's docs are
// explicit that the RAW body is signed: "Do not parse or cast the webhook
// request body." Sign/Verify both operate on []byte for exactly that
// reason, never on a decoded-and-reserialized struct, which would not
// reproduce the exact bytes Razorpay signed.
//
// Shared between services/api-gateway (verifies inbound webhooks,
// docs/PHASE5_5_IMPLEMENTATION.md Unit Z) and scripts/loadgen (signs its own
// synthetic traffic so `make loadgen` keeps working once the Gateway
// requires a valid signature), rather than each keeping its own copy: the
// one thing worse than no signature verification is two implementations
// that quietly drift on what "valid" means.
package webhooksig

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// Sign returns the hex-encoded HMAC-SHA256 of body keyed by secret, exactly
// the value Razorpay puts in X-Razorpay-Signature.
func Sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// Verify reports whether signature is a valid HMAC-SHA256 of body under any
// one of secrets. Passing more than one secret is what makes secret
// rotation work: on rotation, the old secret must still validate a retried
// older request until whatever redelivers it stops, so a caller passes
// [current, previous] rather than only the current value.
//
// An empty signature, or a secrets slice with no non-empty entry, always
// returns false. This is the fail-closed rule made concrete: an unset
// secret must never be treated as "skip verification", it is simply a
// configuration under which nothing can ever verify.
//
// Comparison is constant-time (hmac.Equal), never == or bytes.Equal on the
// hex strings, so a mismatch cannot leak timing information about which
// byte of the expected signature was matched.
func Verify(secrets []string, body []byte, signature string) bool {
	if signature == "" {
		return false
	}
	sigBytes := []byte(signature)
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		expected := []byte(Sign(secret, body))
		if hmac.Equal(expected, sigBytes) {
			return true
		}
	}
	return false
}
