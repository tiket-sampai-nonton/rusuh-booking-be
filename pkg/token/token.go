// Package token issues and verifies compact JWTs signed with Ed25519 (alg EdDSA), tanpa dependensi luar.
// Dipakai untuk access token User Service, admission token Queue, dan token internal antar service (ADR-011).
package token

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Errors returned by Verify.
var (
	ErrMalformed = errors.New("token: format tidak valid")
	ErrSignature = errors.New("token: tanda tangan tidak valid")
	ErrExpired   = errors.New("token: kedaluwarsa")
	ErrClaims    = errors.New("token: klaim tidak sesuai")
)

// Claims are the registered claims RBT uses plus a scope string.
type Claims struct {
	Issuer    string `json:"iss"`
	Subject   string `json:"sub"`
	Audience  string `json:"aud"`
	ID        string `json:"jti,omitempty"`
	Scope     string `json:"scope,omitempty"`
	IssuedAt  int64  `json:"iat"`
	NotBefore int64  `json:"nbf,omitempty"`
	ExpiresAt int64  `json:"exp"`
}

type header struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
	Kid string `json:"kid"`
}

var b64 = base64.RawURLEncoding

// Signer signs tokens with one private key.
type Signer struct {
	key ed25519.PrivateKey
	kid string
}

// NewSigner returns a signer. kid identifies the key for rotation.
func NewSigner(key ed25519.PrivateKey, kid string) (*Signer, error) {
	if len(key) != ed25519.PrivateKeySize {
		return nil, errors.New("token: private key Ed25519 tidak valid")
	}
	if kid == "" {
		return nil, errors.New("token: kid wajib diisi")
	}
	return &Signer{key: key, kid: kid}, nil
}

// Sign encodes and signs c.
func (s *Signer) Sign(c Claims) (string, error) {
	if c.ExpiresAt <= c.IssuedAt {
		return "", fmt.Errorf("%w: exp harus setelah iat", ErrClaims)
	}
	h, err := json.Marshal(header{Alg: "EdDSA", Typ: "JWT", Kid: s.kid})
	if err != nil {
		return "", err
	}
	p, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	signing := b64.EncodeToString(h) + "." + b64.EncodeToString(p)
	sig := ed25519.Sign(s.key, []byte(signing))
	return signing + "." + b64.EncodeToString(sig), nil
}

// Verifier checks signature, issuer, audience, and time.
type Verifier struct {
	keys     map[string]ed25519.PublicKey
	issuer   string
	audience string
	leeway   time.Duration
}

// NewVerifier accepts one or more public keys by kid (rotasi: kunci lama dan baru aktif bersamaan).
func NewVerifier(keys map[string]ed25519.PublicKey, issuer, audience string, leeway time.Duration) (*Verifier, error) {
	if len(keys) == 0 {
		return nil, errors.New("token: minimal satu public key")
	}
	for kid, k := range keys {
		if len(k) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("token: public key %q tidak valid", kid)
		}
	}
	if issuer == "" || audience == "" {
		return nil, errors.New("token: issuer dan audience wajib diisi")
	}
	return &Verifier{keys: keys, issuer: issuer, audience: audience, leeway: leeway}, nil
}

// Verify parses tok and returns its claims when valid at now.
func (v *Verifier) Verify(tok string, now time.Time) (Claims, error) {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return Claims{}, ErrMalformed
	}
	hb, err := b64.DecodeString(parts[0])
	if err != nil {
		return Claims{}, ErrMalformed
	}
	var h header
	if err := json.Unmarshal(hb, &h); err != nil || h.Alg != "EdDSA" {
		return Claims{}, ErrMalformed
	}
	key, ok := v.keys[h.Kid]
	if !ok {
		return Claims{}, ErrSignature
	}
	sig, err := b64.DecodeString(parts[2])
	if err != nil {
		return Claims{}, ErrMalformed
	}
	if !ed25519.Verify(key, []byte(parts[0]+"."+parts[1]), sig) {
		return Claims{}, ErrSignature
	}
	pb, err := b64.DecodeString(parts[1])
	if err != nil {
		return Claims{}, ErrMalformed
	}
	var c Claims
	if err := json.Unmarshal(pb, &c); err != nil {
		return Claims{}, ErrMalformed
	}
	if c.Issuer != v.issuer || c.Audience != v.audience || c.Subject == "" {
		return Claims{}, ErrClaims
	}
	ts := now.Unix()
	lw := int64(v.leeway / time.Second)
	if ts > c.ExpiresAt+lw {
		return Claims{}, ErrExpired
	}
	if c.NotBefore != 0 && ts+lw < c.NotBefore {
		return Claims{}, ErrClaims
	}
	return c, nil
}

// ParsePrivateKeyPEM reads a PKCS#8 "PRIVATE KEY" block holding an Ed25519 key.
func ParsePrivateKeyPEM(data []byte) (ed25519.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("token: PEM private key tidak ditemukan")
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("token: parse private key: %w", err)
	}
	ek, ok := k.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("token: private key bukan Ed25519")
	}
	return ek, nil
}

// ParsePublicKeyPEM reads a PKIX "PUBLIC KEY" block holding an Ed25519 key.
func ParsePublicKeyPEM(data []byte) (ed25519.PublicKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("token: PEM public key tidak ditemukan")
	}
	k, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("token: parse public key: %w", err)
	}
	ek, ok := k.(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("token: public key bukan Ed25519")
	}
	return ek, nil
}
