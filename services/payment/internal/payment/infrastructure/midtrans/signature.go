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
	want := signatureHex(orderID, statusCode, grossAmount, v.serverKey)
	return subtle.ConstantTimeCompare([]byte(want), []byte(strings.ToLower(signatureKey))) == 1
}

// signatureHex is the single home of the Midtrans formula SHA512(order_id + status_code + gross_amount + serverKey).
func signatureHex(orderID, statusCode, grossAmount, serverKey string) string {
	sum := sha512.Sum512([]byte(orderID + statusCode + grossAmount + serverKey))
	return hex.EncodeToString(sum[:])
}

// WebhookSigner implements IWebhookSigner: it signs a notification exactly as Midtrans would, for the local
// simulator. The server key never leaves the adapter.
type WebhookSigner struct{ serverKey string }

// NewWebhookSigner returns a signer for serverKey.
func NewWebhookSigner(serverKey string) *WebhookSigner { return &WebhookSigner{serverKey: serverKey} }

// Sign returns the signature_key; an empty server key signs nothing (the verifier would reject it anyway).
func (s *WebhookSigner) Sign(orderID, statusCode, grossAmount string) string {
	if s.serverKey == "" {
		return ""
	}
	return signatureHex(orderID, statusCode, grossAmount, s.serverKey)
}
