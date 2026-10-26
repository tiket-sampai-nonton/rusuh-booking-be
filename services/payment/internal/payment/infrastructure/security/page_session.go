// Package security signs and verifies the rb_pay cookie: the session a WebView gets after it exchanges a
// one-time page token. The cookie is an EdDSA token for one visitor and one booking.
package security

import (
	"crypto/ed25519"
	"errors"
	"strings"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/rabbitmq"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/token"
)

const (
	// PageAudience separates rb_pay tokens from every other token Payment signs (service tokens).
	PageAudience = "rbt-payment-page"
	// PageSessionTTL is how long a WebView session lasts. The payment window is 10 minutes; the session may
	// outlive it by a little so the result screen still loads.
	PageSessionTTL = 15 * time.Minute

	scopePrefix = "booking:"
)

// PageSession implements outbound.ISessionSigner and verifies the cookie it signs.
type PageSession struct {
	signer   *token.Signer
	verifier *token.Verifier
	issuer   string
}

// NewPageSession builds the signer and verifier from the Payment private key (the public key is derived).
func NewPageSession(key ed25519.PrivateKey, keyID, issuer string) (*PageSession, error) {
	signer, err := token.NewSigner(key, keyID)
	if err != nil {
		return nil, err
	}
	pub, ok := key.Public().(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("page session: kunci publik tidak bisa diturunkan")
	}
	verifier, err := token.NewVerifier(map[string]ed25519.PublicKey{keyID: pub}, issuer, PageAudience, 5*time.Second)
	if err != nil {
		return nil, err
	}
	return &PageSession{signer: signer, verifier: verifier, issuer: issuer}, nil
}

// Sign returns the cookie value for visitorID on bookingID and when it expires.
func (p *PageSession) Sign(visitorID, bookingID string, now time.Time) (string, time.Time, error) {
	exp := now.Add(PageSessionTTL)
	tok, err := p.signer.Sign(token.Claims{
		Issuer: p.issuer, Subject: visitorID, Audience: PageAudience, ID: rabbitmq.NewID(),
		Scope: scopePrefix + bookingID, IssuedAt: now.Unix(), ExpiresAt: exp.Unix(),
	})
	if err != nil {
		return "", time.Time{}, err
	}
	return tok, exp, nil
}

// Verify checks the cookie and returns the visitor and the one booking it is valid for.
func (p *PageSession) Verify(raw string, now time.Time) (visitorID, bookingID string, err error) {
	c, err := p.verifier.Verify(raw, now)
	if err != nil {
		return "", "", err
	}
	bookingID, ok := strings.CutPrefix(c.Scope, scopePrefix)
	if !ok || bookingID == "" {
		return "", "", token.ErrClaims
	}
	return c.Subject, bookingID, nil
}
