// Package crypto holds the field-level protection used for PII: HMAC-SHA256 untuk pencarian
// (hash deterministik) dan AES-256-GCM untuk penyimpanan yang bisa dibuka kembali.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// ErrDecrypt is returned when ciphertext is corrupt or the key is wrong.
var ErrDecrypt = errors.New("crypto: gagal membuka ciphertext")

// ParseKey decodes a 32-byte hex key (64 karakter), mis. dari PII_ENC_KEY.
func ParseKey(hexKey string) ([]byte, error) {
	k, err := hex.DecodeString(strings.TrimSpace(hexKey))
	if err != nil {
		return nil, fmt.Errorf("crypto: key bukan hex: %w", err)
	}
	if len(k) != 32 {
		return nil, fmt.Errorf("crypto: key harus 32 byte, didapat %d", len(k))
	}
	return k, nil
}

// Hasher computes keyed hashes for lookup columns (email_hash, nik_hash).
type Hasher struct{ key []byte }

// NewHasher returns a hasher with a 32-byte key.
func NewHasher(key []byte) (*Hasher, error) {
	if len(key) != 32 {
		return nil, errors.New("crypto: HMAC key harus 32 byte")
	}
	return &Hasher{key: append([]byte(nil), key...)}, nil
}

// Hash returns HMAC-SHA256(key, value).
func (h *Hasher) Hash(value string) []byte {
	m := hmac.New(sha256.New, h.key)
	_, _ = m.Write([]byte(value))
	return m.Sum(nil)
}

// Sealer encrypts and decrypts with AES-256-GCM. Output = nonce || ciphertext || tag.
type Sealer struct{ aead cipher.AEAD }

// NewSealer returns a sealer with a 32-byte key.
func NewSealer(key []byte) (*Sealer, error) {
	if len(key) != 32 {
		return nil, errors.New("crypto: AES key harus 32 byte")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Sealer{aead: aead}, nil
}

// Seal encrypts plaintext. aad mengikat ciphertext ke konteksnya (mis. "accounts.nik").
func (s *Sealer) Seal(plaintext, aad []byte) ([]byte, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("crypto: nonce: %w", err)
	}
	return s.aead.Seal(nonce, nonce, plaintext, aad), nil
}

// Open decrypts data produced by Seal with the same aad.
func (s *Sealer) Open(data, aad []byte) ([]byte, error) {
	n := s.aead.NonceSize()
	if len(data) < n+s.aead.Overhead() {
		return nil, ErrDecrypt
	}
	out, err := s.aead.Open(nil, data[:n], data[n:], aad)
	if err != nil {
		return nil, ErrDecrypt
	}
	return out, nil
}

// MaskEmail returns e.g. "ar***@gmail.com".
func MaskEmail(email string) string {
	local, domain, ok := strings.Cut(email, "@")
	if !ok || local == "" {
		return "***"
	}
	keep := 2
	if len(local) <= 2 {
		keep = 1
	}
	return local[:keep] + "***@" + domain
}

// MaskNIK returns e.g. "3174********0001" for a 16-digit NIK.
func MaskNIK(nik string) string {
	if len(nik) != 16 {
		return "****************"
	}
	return nik[:4] + strings.Repeat("*", 8) + nik[12:]
}
