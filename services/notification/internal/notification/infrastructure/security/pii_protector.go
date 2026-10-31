// Package security implements IPIIProtector with pkg/crypto. Keys are shared with the User Service
// so otp.requested ciphertext opens here with the same AAD.
package security

import (
	"encoding/base64"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/crypto"
)

// AAD labels bind ciphertext to its context. The email/OTP labels MUST match the User Service.
const (
	aadEmail     = "users.accounts.email"
	aadOTP       = "otp.requested.code"
	aadPushToken = "notification.push_token"
)

// PII implements outbound.IPIIProtector.
type PII struct {
	sealer *crypto.Sealer
	hasher *crypto.Hasher
}

// NewPII builds the protector. encKey seals/opens AES-256-GCM; hashKey keys the recipient HMAC.
func NewPII(encKey, hashKey []byte) (*PII, error) {
	s, err := crypto.NewSealer(encKey)
	if err != nil {
		return nil, err
	}
	h, err := crypto.NewHasher(hashKey)
	if err != nil {
		return nil, err
	}
	return &PII{sealer: s, hasher: h}, nil
}

// UnsealEmail decrypts an email sealed by the User Service (AAD users.accounts.email).
func (p *PII) UnsealEmail(enc []byte) (string, error) {
	out, err := p.sealer.Open(enc, []byte(aadEmail))
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// UnsealBuyerEmail opens the base64 buyer email sealed by Queue (AAD crypto.AADBuyerEmail), carried in booking.* events.
func (p *PII) UnsealBuyerEmail(enc string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return "", crypto.ErrDecrypt
	}
	out, err := p.sealer.Open(raw, []byte(crypto.AADBuyerEmail))
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// UnsealOTP decrypts an OTP code sealed by the User Service (AAD otp.requested.code).
func (p *PII) UnsealOTP(enc []byte) (string, error) {
	out, err := p.sealer.Open(enc, []byte(aadOTP))
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// SealPushToken encrypts an FCM device token for push_token_enc.
func (p *PII) SealPushToken(token string) ([]byte, error) {
	return p.sealer.Seal([]byte(token), []byte(aadPushToken))
}

// UnsealPushToken decrypts a stored FCM device token.
func (p *PII) UnsealPushToken(enc []byte) (string, error) {
	out, err := p.sealer.Open(enc, []byte(aadPushToken))
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// HMACRecipient returns HMAC("recipient:"+value) for recipient_hash (email or visitor id).
func (p *PII) HMACRecipient(value string) []byte {
	return p.hasher.Hash("recipient:" + value)
}
