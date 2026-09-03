package webhooksig

import "testing"

func TestVerifyAcceptsAValidSignature(t *testing.T) {
	body := []byte(`{"event":"payment.failed"}`)
	sig := Sign("wh-secret", body)

	if !Verify([]string{"wh-secret"}, body, sig) {
		t.Fatal("Verify() = false for a correctly signed body, want true")
	}
}

func TestVerifyRejectsAnInvalidSignature(t *testing.T) {
	body := []byte(`{"event":"payment.failed"}`)

	if Verify([]string{"wh-secret"}, body, "0000000000000000000000000000000000000000000000000000000000000000") {
		t.Fatal("Verify() = true for a wrong signature, want false")
	}
}

func TestVerifyRejectsAMissingSignature(t *testing.T) {
	body := []byte(`{"event":"payment.failed"}`)

	if Verify([]string{"wh-secret"}, body, "") {
		t.Fatal("Verify() = true for an empty signature, want false")
	}
}

// TestVerifyRejectsABodyAlteredAfterSigning is the exact failure mode a
// decode-then-reserialize implementation would miss: the signature was
// computed over the original bytes, and even a single byte's difference in
// the body presented for verification must fail.
func TestVerifyRejectsABodyAlteredAfterSigning(t *testing.T) {
	original := []byte(`{"event":"payment.failed","amount":50000}`)
	sig := Sign("wh-secret", original)

	altered := []byte(`{"event":"payment.failed","amount":50001}`)
	if Verify([]string{"wh-secret"}, altered, sig) {
		t.Fatal("Verify() = true for a body altered after signing, want false")
	}
}

// TestVerifyAcceptsThePreviousSecretDuringRotation proves the documented
// rotation guarantee (razorpay.com/docs/webhooks/validate-test/): a retried
// older request, signed under a secret that has since been rotated out,
// must still verify as long as the previous secret is still configured.
func TestVerifyAcceptsThePreviousSecretDuringRotation(t *testing.T) {
	body := []byte(`{"event":"payment.failed"}`)
	oldSig := Sign("old-secret", body)

	if !Verify([]string{"new-secret", "old-secret"}, body, oldSig) {
		t.Fatal("Verify() = false for a signature under the previous secret, want true (rotation must still validate retried older requests)")
	}
}

func TestVerifyRejectsWhenNoSecretIsConfigured(t *testing.T) {
	body := []byte(`{"event":"payment.failed"}`)
	sig := Sign("whatever", body)

	// An unset secret must never mean "skip verification": with no usable
	// secret configured, every signature is rejected, never accepted.
	if Verify(nil, body, sig) {
		t.Fatal("Verify() = true with no secrets configured, want false (fail closed)")
	}
	if Verify([]string{""}, body, sig) {
		t.Fatal("Verify() = true with only an empty-string secret, want false (fail closed)")
	}
}

func TestSignIsDeterministic(t *testing.T) {
	body := []byte(`{"event":"payment.failed"}`)
	if Sign("wh-secret", body) != Sign("wh-secret", body) {
		t.Fatal("Sign() is not deterministic for the same secret and body")
	}
}

func TestSignDiffersByBody(t *testing.T) {
	sig1 := Sign("wh-secret", []byte("a"))
	sig2 := Sign("wh-secret", []byte("b"))
	if sig1 == sig2 {
		t.Fatal("Sign() produced the same signature for two different bodies")
	}
}
