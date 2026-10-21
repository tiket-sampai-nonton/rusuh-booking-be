package outbound

//go:generate go tool mockgen -source=pii_protector.go -destination=../../../../_mock/notification/port/outbound/pii_protector_mock.go -package=mock_outbound

// IPIIProtector decrypts PII carried in events and derives recipient hashes. Keys are shared
// with the User Service so ciphertext produced there opens here (same AAD).
type IPIIProtector interface {
	// UnsealEmail decrypts an email sealed with AAD "users.accounts.email".
	UnsealEmail(enc []byte) (string, error)
	// UnsealOTP decrypts an OTP code sealed with AAD "otp.requested.code".
	UnsealOTP(enc []byte) (string, error)
	// SealPushToken encrypts an FCM device token for storage (push_token_enc).
	SealPushToken(token string) ([]byte, error)
	// UnsealPushToken decrypts a stored FCM device token before sending.
	UnsealPushToken(enc []byte) (string, error)
	// HMACRecipient returns the HMAC of a recipient (email or visitor id) for recipient_hash.
	HMACRecipient(value string) []byte
}
