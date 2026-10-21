package security

import (
	"net/http"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/token"
)

const cookieVisitor = "rb_visitor"

// VisitorAuthenticator verifies the EdDSA visitor token from the rb_visitor cookie or the
// X-Visitor-Token header and returns its subject (the visitor id). The public key is shared with
// the Queue Service that mints the token.
type VisitorAuthenticator struct {
	verifier *token.Verifier
	now      func() time.Time
}

// NewVisitorAuthenticator returns the authenticator.
func NewVisitorAuthenticator(verifier *token.Verifier, now func() time.Time) *VisitorAuthenticator {
	return &VisitorAuthenticator{verifier: verifier, now: now}
}

// Resolve returns the verified visitor id and whether a valid token was present.
func (a *VisitorAuthenticator) Resolve(r *http.Request) (string, bool) {
	raw := ""
	if c, err := r.Cookie(cookieVisitor); err == nil && c.Value != "" {
		raw = c.Value
	} else if h := r.Header.Get("X-Visitor-Token"); h != "" {
		raw = h
	}
	if raw == "" {
		return "", false
	}
	claims, err := a.verifier.Verify(raw, a.now())
	if err != nil {
		return "", false
	}
	return claims.Subject, true
}
