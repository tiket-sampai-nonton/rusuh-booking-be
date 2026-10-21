// Package security implements the PII protector and token issuer/verifier ports using pkg/crypto
// and pkg/token. NIK is only ever stored as HMAC; email is encrypted (design §12).
package security

import (
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/crypto"
)

const emailAAD = "queue.email"

// PII implements outbound.IPIIProtector.
type PII struct {
	hasher *crypto.Hasher
	sealer *crypto.Sealer
}

// NewPII builds the protector from the 32-byte HMAC and AES keys.
func NewPII(encKey, hashKey []byte) (*PII, error) {
	h, err := crypto.NewHasher(hashKey)
	if err != nil {
		return nil, err
	}
	s, err := crypto.NewSealer(encKey)
	if err != nil {
		return nil, err
	}
	return &PII{hasher: h, sealer: s}, nil
}

// HashNIK returns HMAC-SHA256(NIK).
func (p *PII) HashNIK(nik string) []byte { return p.hasher.Hash(nik) }

// HashEmail returns HMAC-SHA256(email).
func (p *PII) HashEmail(email string) []byte { return p.hasher.Hash(email) }

// SealEmail encrypts the email with AES-256-GCM.
func (p *PII) SealEmail(email string) ([]byte, error) {
	return p.sealer.Seal([]byte(email), []byte(emailAAD))
}

// UnsealEmail decrypts an email sealed by SealEmail.
func (p *PII) UnsealEmail(enc []byte) (string, error) {
	out, err := p.sealer.Open(enc, []byte(emailAAD))
	if err != nil {
		return "", err
	}
	return string(out), nil
}
