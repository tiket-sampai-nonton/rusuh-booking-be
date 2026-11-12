// Package security implements the cryptographic driven ports: PII protection, token issuing, and secrets.
package security

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"math/big"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/crypto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/revocation"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/token"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/port/inbound"
)

// PII implements outbound.IPIIProtector.
type PII struct {
	hasher    *crypto.Hasher
	otpHasher *crypto.Hasher
	sealer    *crypto.Sealer
}

// NewPII builds the protector from three distinct 32-byte keys.
func NewPII(encKey, hashKey, otpKey []byte) (*PII, error) {
	h, err := crypto.NewHasher(hashKey)
	if err != nil {
		return nil, err
	}
	o, err := crypto.NewHasher(otpKey)
	if err != nil {
		return nil, err
	}
	s, err := crypto.NewSealer(encKey)
	if err != nil {
		return nil, err
	}
	return &PII{hasher: h, otpHasher: o, sealer: s}, nil
}

// HashEmail returns HMAC(email) for users.accounts.email_hash.
func (p *PII) HashEmail(email string) []byte { return p.hasher.Hash("email:" + email) }

// SealEmail encrypts the email.
func (p *PII) SealEmail(email string) ([]byte, error) {
	return p.sealer.Seal([]byte(email), []byte("users.accounts.email"))
}

// UnsealEmail decrypts the email.
func (p *PII) UnsealEmail(enc []byte) (string, error) {
	data, err := p.sealer.Open(enc, []byte("users.accounts.email"))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// HashNIK returns HMAC(nik); kunci yang sama dipakai Queue/Booking untuk buyer_nik_hash sehingga hasilnya bisa dicocokkan.
func (p *PII) HashNIK(nik string) []byte { return p.hasher.Hash("nik:" + nik) }

// SealNIK encrypts the NIK.
func (p *PII) SealNIK(nik string) ([]byte, error) {
	return p.sealer.Seal([]byte(nik), []byte("users.accounts.nik"))
}

// UnsealNIK decrypts the NIK.
func (p *PII) UnsealNIK(enc []byte) (string, error) {
	data, err := p.sealer.Open(enc, []byte("users.accounts.nik"))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// HashOTP binds the code to the email so a code for one email is useless for another.
func (p *PII) HashOTP(emailHash []byte, code string) []byte {
	return p.otpHasher.Hash(fmt.Sprintf("%x:%s", emailHash, code))
}

// SealOTP encrypts the OTP code for transport in the otp.requested event payload.
func (p *PII) SealOTP(code string) ([]byte, error) {
	return p.sealer.Seal([]byte(code), []byte("otp.requested.code"))
}

// HashRefreshToken returns SHA-256 of a high-entropy random token (tidak perlu kunci).
func (p *PII) HashRefreshToken(raw string) []byte {
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}

// Secrets implements outbound.ISecretGenerator with crypto/rand.
type Secrets struct{}

// OTPCode returns a uniform 6-digit code.
func (Secrets) OTPCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", fmt.Errorf("security: otp: %w", err)
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

// RefreshToken returns 32 random bytes, base64url.
func (Secrets) RefreshToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("security: refresh token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// SessionID returns a UUIDv7 (RFC 9562, ADR-032), so the access token (claim sid) can be issued before the session row is
// committed and session ids sort by creation time.
func (Secrets) SessionID() (string, error) {
	var r [10]byte // 4 version + 12 random, 2 variant + 14 random, 48 random
	if _, err := rand.Read(r[:]); err != nil {
		return "", fmt.Errorf("security: session id: %w", err)
	}
	r[0] = (r[0] & 0x0f) | 0x70
	r[2] = (r[2] & 0x3f) | 0x80
	ts := fmt.Sprintf("%012x", time.Now().UnixMilli()&0xffffffffffff) // printed as hex: no integer conversion to overflow
	return fmt.Sprintf("%s-%s-%x-%x-%x", ts[:8], ts[8:], r[0:2], r[2:4], r[4:10]), nil
}

// Tokens implements outbound.ITokenIssuer and inbound.IAccessTokenVerifier.
type Tokens struct {
	signer   *token.Signer
	verifier *token.Verifier
	issuer   string
	audience string
	ttl      time.Duration
	now      func() time.Time
	denylist revocation.Denylist // nil = pengecekan logout tidak aktif
}

// NewTokens builds the issuer/verifier pair. Public key dibagikan ke Queue dan Booking untuk memverifikasi token yang sama.
func NewTokens(signer *token.Signer, verifier *token.Verifier, issuer, audience string, ttl time.Duration, now func() time.Time) *Tokens {
	return &Tokens{signer: signer, verifier: verifier, issuer: issuer, audience: audience, ttl: ttl, now: now}
}

// WithDenylist makes VerifyAccess refuse tokens of sessions that logged out (and, if the denylist cannot be read, all
// tokens: fail closed).
func (t *Tokens) WithDenylist(d revocation.Denylist) *Tokens {
	t.denylist = d
	return t
}

// IssueAccess signs an access token for accountID with identity claims.
func (t *Tokens) IssueAccess(accountID, sessionID, email string, nikHash []byte, nikMasked string, now time.Time) (string, time.Time, error) {
	exp := now.Add(t.ttl)
	jti, err := Secrets{}.RefreshToken()
	if err != nil {
		return "", time.Time{}, err
	}
	claims := token.Claims{
		Issuer:    t.issuer,
		Subject:   accountID,
		Audience:  t.audience,
		ID:        jti[:22],
		Scope:     "buyer",
		SessionID: sessionID,
		IssuedAt:  now.Unix(),
		ExpiresAt: exp.Unix(),
		Email:     email,
		HasNIK:    len(nikHash) > 0,
	}
	if len(nikHash) > 0 {
		claims.NIKHash = fmt.Sprintf("%x", nikHash)
		claims.NIKMasked = nikMasked
	}
	tok, err := t.signer.Sign(claims)
	return tok, exp, err
}

// VerifyAccess validates a Bearer token.
func (t *Tokens) VerifyAccess(tok string) (string, error) {
	c, err := t.verifier.Verify(tok, t.now())
	if err != nil || revocation.Check(t.denylist, c) != nil {
		return "", inbound.ErrUnauthorized
	}
	return c.Subject, nil
}
