package security

import (
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/token"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// Token scopes minted by the queue service.
const (
	ScopeAdmission = "admission"
	ScopeVisitor   = "visitor"
	ScopeQueue     = "queue"
)

// TokenIssuer implements outbound.ITokenIssuer using an EdDSA signer (pkg/token).
type TokenIssuer struct {
	signer       *token.Signer
	issuer       string
	audience     string
	admissionTTL time.Duration
	visitorTTL   time.Duration
	queueTTL     time.Duration
}

// NewTokenIssuer returns the issuer.
func NewTokenIssuer(signer *token.Signer, issuer, audience string, admissionTTL, visitorTTL, queueTTL time.Duration) *TokenIssuer {
	return &TokenIssuer{
		signer: signer, issuer: issuer, audience: audience,
		admissionTTL: admissionTTL, visitorTTL: visitorTTL, queueTTL: queueTTL,
	}
}

func (t *TokenIssuer) sign(subject, scope string, now time.Time, ttl time.Duration) (string, time.Time, error) {
	exp := now.Add(ttl)
	tok, err := t.signer.Sign(token.Claims{
		Issuer: t.issuer, Subject: subject, Audience: t.audience, Scope: scope,
		IssuedAt: now.Unix(), ExpiresAt: exp.Unix(),
	})
	return tok, exp, err
}

// IssueAdmission mints a one-time admission token (TTL 60s).
// jti=allocationID so Booking can activate the admission record by its primary key.
func (t *TokenIssuer) IssueAdmission(entryID, eventID, allocationID string, now time.Time) (string, time.Time, error) {
	exp := now.Add(t.admissionTTL)
	tok, err := t.signer.Sign(token.Claims{
		Issuer: t.issuer, Subject: entryID, Audience: t.audience, Scope: ScopeAdmission,
		ID: allocationID, IssuedAt: now.Unix(), ExpiresAt: exp.Unix(),
	})
	return tok, exp, err
}

// IssueVisitor mints a visitor token identifying a device.
func (t *TokenIssuer) IssueVisitor(visitorID string, now time.Time) (string, error) {
	tok, _, err := t.sign(visitorID, ScopeVisitor, now, t.visitorTTL)
	return tok, err
}

// IssueQueue mints a queue token for an entry.
func (t *TokenIssuer) IssueQueue(entryID, visitorID string, now time.Time) (string, error) {
	tok, _, err := t.sign(entryID, ScopeQueue, now, t.queueTTL)
	return tok, err
}

// IssueService mints a long-lived service bearer token for service-to-service calls.
// Subject is "queue-service"; Booking verifies with Queue's public key.
func (t *TokenIssuer) IssueService(now time.Time) (string, error) {
	tok, _, err := t.sign("queue-service", "service", now, 24*time.Hour)
	return tok, err
}

// UserTokenVerifier implements outbound.IUserTokenVerifier against the User Service public key.
type UserTokenVerifier struct {
	verifier *token.Verifier
	now      func() time.Time
}

// NewUserTokenVerifier returns the verifier.
func NewUserTokenVerifier(verifier *token.Verifier, now func() time.Time) *UserTokenVerifier {
	return &UserTokenVerifier{verifier: verifier, now: now}
}

// Verify checks a User Service bearer token and extracts the buyer identity.
func (v *UserTokenVerifier) Verify(bearer string) (outbound.UserIdentity, error) {
	claims, err := v.verifier.Verify(bearer, v.now())
	if err != nil {
		return outbound.UserIdentity{}, err
	}
	return outbound.UserIdentity{
		AccountID: claims.Subject,
		HasNIK:    claims.HasNIK,
		NIKHash:   claims.NIKHash,
		Email:     claims.Email,
	}, nil
}
