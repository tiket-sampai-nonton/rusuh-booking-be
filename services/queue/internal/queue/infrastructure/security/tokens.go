package security

import (
	"sync"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/revocation"

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

	svcMu  sync.Mutex // melindungi cache token service
	svcTok string
	svcExp time.Time
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

const (
	serviceTokenTTL     = 5 * time.Minute  // batas token service antar-service (httpx.ServiceTokenMaxTTL)
	serviceTokenRefresh = 30 * time.Second // tanda tangan ulang bila sisa umur kurang dari ini
)

// ServiceToken returns the bearer token Queue sends to Booking's /internal/v1/*. It lives 5 minutes and is signed
// again shortly before it expires, so a long-running worker never keeps an expired token. Subject is
// "queue-service"; Booking verifies it with Queue's public key.
func (t *TokenIssuer) ServiceToken(now time.Time) (string, error) {
	t.svcMu.Lock()
	defer t.svcMu.Unlock()
	if t.svcTok != "" && t.svcExp.Sub(now) > serviceTokenRefresh {
		return t.svcTok, nil
	}
	tok, exp, err := t.sign("queue-service", "service", now, serviceTokenTTL)
	if err != nil {
		return "", err
	}
	t.svcTok, t.svcExp = tok, exp
	return tok, nil
}

// UserTokenVerifier implements outbound.IUserTokenVerifier against the User Service public key.
type UserTokenVerifier struct {
	verifier *token.Verifier
	now      func() time.Time
	deny     revocation.Denylist
}

// NewUserTokenVerifier returns the verifier.
func NewUserTokenVerifier(verifier *token.Verifier, now func() time.Time) *UserTokenVerifier {
	return &UserTokenVerifier{verifier: verifier, now: now}
}

// WithDenylist refuses tokens whose session logged out (and every token when the denylist cannot be read: fail closed).
func (v *UserTokenVerifier) WithDenylist(d revocation.Denylist) *UserTokenVerifier {
	v.deny = d
	return v
}

// Verify checks a User Service bearer token and extracts the buyer identity.
func (v *UserTokenVerifier) Verify(bearer string) (outbound.UserIdentity, error) {
	claims, err := v.verifier.Verify(bearer, v.now())
	if err != nil {
		return outbound.UserIdentity{}, err
	}
	if err := revocation.Check(v.deny, claims); err != nil {
		return outbound.UserIdentity{}, err
	}
	return outbound.UserIdentity{
		AccountID: claims.Subject,
		HasNIK:    claims.HasNIK,
		NIKMasked: claims.NIKMasked,
		NIKHash:   claims.NIKHash,
		Email:     claims.Email,
	}, nil
}
