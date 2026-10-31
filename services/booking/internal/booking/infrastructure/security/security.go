// Package security implements the Booking outbound security ports: the NIK protector (HMAC hash +
// AES-256-GCM seal + mask, over pkg/crypto) and the admission-token verifier (EdDSA, pkg/token).
package security

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
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
	// The Queue key also signs visitor, queue, and service tokens; only an admission token may open a session.
	if claims.Scope != ScopeAdmission {
		return outbound.AdmissionClaims{}, errors.New("security: bukan admission token")
	}
	if claims.ID == "" && claims.Subject == "" {
		return outbound.AdmissionClaims{}, errors.New("security: admission token tanpa sub")
	}
	return outbound.AdmissionClaims{
		JTI:       claims.ID,
		EventID:   claims.Audience, // may be empty if aud=global; session usecase falls back to DB lookup
		VisitorID: "",              // admission token sub=entryId, not visitor_id; visitor check skipped
	}, nil
}

// Token scopes. Each key signs several kinds of token; every guard names the one kind it accepts.
const (
	ScopeAdmission = "admission" // Queue -> buyer, opens a booking session
	ScopeService   = "service"   // Queue -> Booking, internal calls
	ScopeBuyer     = "buyer"     // User Service access token
)

// BearerVerifier verifies a bearer token of exactly one scope and returns its subject. It satisfies the
// http.IUserTokenVerifier interface structurally.
type BearerVerifier struct {
	verifier *token.Verifier
	now      func() time.Time
	scope    string
	admins   map[string]bool // lower-cased emails, or "@domain" entries; nil = not an admin check
}

// NewBearerVerifier builds a verifier that accepts only tokens with the given scope.
func NewBearerVerifier(verifier *token.Verifier, now func() time.Time, scope string) *BearerVerifier {
	return &BearerVerifier{verifier: verifier, now: now, scope: scope}
}

// NewAdminVerifier accepts a buyer (User Service) token whose email is in the allow-list (ADMIN_EMAILS).
// An entry "@domain" admits every address at that domain (only for a domain you control, e.g. local E2E). An empty
// list admits nobody.
func NewAdminVerifier(verifier *token.Verifier, now func() time.Time, emails []string) *BearerVerifier {
	set := map[string]bool{}
	for _, e := range emails {
		if e = strings.ToLower(strings.TrimSpace(e)); e != "" {
			set[e] = true
		}
	}
	return &BearerVerifier{verifier: verifier, now: now, scope: ScopeBuyer, admins: set}
}

// VerifyAccess returns the token subject when the token is valid, has the right scope, and (for admin) is allow-listed.
func (v *BearerVerifier) VerifyAccess(tok string) (string, error) {
	if v == nil || v.verifier == nil {
		return "", errors.New("security: token verifier belum dikonfigurasi")
	}
	claims, err := v.verifier.Verify(tok, v.now())
	if err != nil {
		return "", err
	}
	if claims.Scope != v.scope {
		return "", errors.New("security: scope token tidak sesuai")
	}
	if v.admins != nil && !v.isAdmin(claims.Email) {
		return "", errors.New("security: bukan admin")
	}
	return claims.Subject, nil
}

func (v *BearerVerifier) isAdmin(email string) bool {
	email = strings.ToLower(email)
	if v.admins[email] {
		return true
	}
	if at := strings.LastIndex(email, "@"); at > 0 {
		return v.admins[email[at:]]
	}
	return false
}

// TicketLinks signs and verifies the ticket-link token in the e-ticket email: HMAC-SHA256(bookingID), so the link only
// opens the tickets of the booking it was issued for and cannot be derived from the booking id.
type TicketLinks struct{ key []byte }

// NewTicketLinks builds the signer; the key must be at least 16 bytes.
func NewTicketLinks(key []byte) (*TicketLinks, error) {
	if len(key) < 16 {
		return nil, errors.New("security: kunci tautan tiket minimal 16 byte")
	}
	return &TicketLinks{key: key}, nil
}

// Token returns the base64url token for a booking.
func (l *TicketLinks) Token(bookingID string) string {
	m := hmac.New(sha256.New, l.key)
	m.Write([]byte("ticket-link:" + bookingID))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// Valid reports whether token was issued for bookingID (constant time).
func (l *TicketLinks) Valid(bookingID, token string) bool {
	return hmac.Equal([]byte(l.Token(bookingID)), []byte(token))
}
