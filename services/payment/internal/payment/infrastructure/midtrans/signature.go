package midtrans

import (
	"crypto/sha512"
	"crypto/subtle"
	"encoding/hex"
	"strings"
)

// SignatureVerifier implements IWebhookVerifier: signature_key = SHA512(order_id + status_code + gross_amount + ServerKey).
// A verified signature is the only proof of payment (invariant I-1).
type SignatureVerifier struct{ serverKey string }

// NewSignatureVerifier returns a verifier for serverKey.
func NewSignatureVerifier(serverKey string) *SignatureVerifier {
	return &SignatureVerifier{serverKey: serverKey}
}

// Verify compares in constant time. An empty server key or empty signature never verifies.
func (v *SignatureVerifier) Verify(orderID, statusCode, grossAmount, signatureKey string) bool {
	if v.serverKey == "" || signatureKey == "" {
		return false
	}
	sum := sha512.Sum512([]byte(orderID + statusCode + grossAmount + v.serverKey))
	want := hex.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(want), []byte(strings.ToLower(signatureKey))) == 1
}
