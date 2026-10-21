// Package security implements the Booking outbound security ports: the NIK protector (HMAC hash +
// AES-256-GCM seal + mask, over pkg/crypto) and the admission-token verifier (EdDSA, pkg/token).
package security

import (
	"errors"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/crypto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/token"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

const aadNIK = "booking.attendee_nik"

// PII hashes, seals, and masks NIK values. It satisfies valueobject.NIKProtector.
type PII struct {
	hasher *crypto.Hasher
	sealer *crypto.Sealer
}

// NewPII builds the protector from 32-byte HMAC and AES keys.
func NewPII(hashKey, encKey []byte) (*PII, error) {
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

// HashNIK returns the HMAC lookup hash.
func (p *PII) HashNIK(nik string) []byte { return p.hasher.Hash(nik) }

// SealNIK returns the AES-256-GCM ciphertext bound to the NIK column context.
func (p *PII) SealNIK(nik string) ([]byte, error) { return p.sealer.Seal([]byte(nik), []byte(aadNIK)) }

// AdmissionVerifier verifies the one-time EdDSA admission token minted by the Queue Service. It
// satisfies outbound.IAdmissionTokenVerifier.
type AdmissionVerifier struct {
	verifier *token.Verifier
	now      func() time.Time
}

// NewAdmissionVerifier builds the verifier.
func NewAdmissionVerifier(verifier *token.Verifier, now func() time.Time) *AdmissionVerifier {
	return &AdmissionVerifier{verifier: verifier, now: now}
}

// Verify parses the token and returns its admission claims.
// The token sub=entryId; the session usecase looks up eventId from the admission record.
func (v *AdmissionVerifier) Verify(tok string) (outbound.AdmissionClaims, error) {
	if v.verifier == nil {
		return outbound.AdmissionClaims{}, errors.New("security: token verifier belum dikonfigurasi (TOKEN_PUBLIC_KEY_PATH)")
	}
	claims, err := v.verifier.Verify(tok, v.now())
	if err != nil {
		return outbound.AdmissionClaims{}, err
	}
	if claims.ID == "" && claims.Subject == "" {
		return outbound.AdmissionClaims{}, errors.New("security: admission token tanpa sub")
	}
	return outbound.AdmissionClaims{
		JTI:       claims.ID,
		EventID:   claims.Audience, // may be empty if aud=global; session usecase falls back to DB lookup
		VisitorID: claims.Subject,
	}, nil
}

// BearerVerifier verifies a User/service access token and returns its subject (account/service id).
// It satisfies the http.IUserTokenVerifier interface structurally.
type BearerVerifier struct {
	verifier *token.Verifier
	now      func() time.Time
}

// NewBearerVerifier builds the verifier.
func NewBearerVerifier(verifier *token.Verifier, now func() time.Time) *BearerVerifier {
	return &BearerVerifier{verifier: verifier, now: now}
}

// VerifyAccess returns the token subject when the token is valid.
func (v *BearerVerifier) VerifyAccess(tok string) (string, error) {
	if v.verifier == nil {
		return "", errors.New("security: token verifier belum dikonfigurasi (TOKEN_PUBLIC_KEY_PATH)")
	}
	claims, err := v.verifier.Verify(tok, v.now())
	if err != nil {
		return "", err
	}
	return claims.Subject, nil
}

// MultiBearerVerifier tries two verifiers in order (user bearer first, then service bearer).
// This lets Booking accept both User buyer tokens (/me/bookings) and Queue service tokens
// (/internal/v1/allocations:batch) from a single middleware.
type MultiBearerVerifier struct {
	primary   *BearerVerifier
	secondary *BearerVerifier
}

// NewMultiBearerVerifier builds the multi-verifier. Either may be nil (treated as absent).
func NewMultiBearerVerifier(primary, secondary *token.Verifier, now func() time.Time) *MultiBearerVerifier {
	return &MultiBearerVerifier{
		primary:   NewBearerVerifier(primary, now),
		secondary: NewBearerVerifier(secondary, now),
	}
}

// VerifyAccess tries the primary verifier first; falls back to secondary on failure.
func (v *MultiBearerVerifier) VerifyAccess(tok string) (string, error) {
	if sub, err := v.primary.VerifyAccess(tok); err == nil {
		return sub, nil
	}
	return v.secondary.VerifyAccess(tok)
}
