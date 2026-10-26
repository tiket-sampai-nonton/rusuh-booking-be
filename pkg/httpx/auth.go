package httpx

import (
	"context"
	"net/http"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/apperror"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/token"
)

// Visitor token transport: web sends the cookie, mobile sends the header (same token, same checks).
const (
	CookieVisitor      = "rb_visitor"
	HeaderVisitorToken = "X-Visitor-Token"
)

// ServiceTokenMaxTTL is the longest lifetime an internal service token may have (R2.3).
const ServiceTokenMaxTTL = 5 * time.Minute

// TokenVerifier is satisfied by *token.Verifier (one issuer, one audience, one key set).
type TokenVerifier interface {
	Verify(tok string, now time.Time) (token.Claims, error)
}

// Visitor is the verified buyer device behind a visitor token.
type Visitor struct {
	ID           string // token subject
	BuyerNikHash string // HMAC of the buyer NIK, empty when the visitor has no NIK
	Email        string
}

// ServiceCaller is the verified calling service behind a service token.
type ServiceCaller struct {
	Issuer  string // calling service, e.g. "rbt-booking"
	Subject string
}

type visitorKey struct{}
type serviceKey struct{}

// VisitorFrom returns the visitor stored by VisitorAuth.
func VisitorFrom(ctx context.Context) (Visitor, bool) {
	v, ok := ctx.Value(visitorKey{}).(Visitor)
	return v, ok
}

// ServiceFrom returns the calling service stored by ServiceAuth.
func ServiceFrom(ctx context.Context) (ServiceCaller, bool) {
	c, ok := ctx.Value(serviceKey{}).(ServiceCaller)
	return c, ok
}

func unauthorized(w http.ResponseWriter, r *http.Request, scheme string, cause error) {
	w.Header().Set("WWW-Authenticate", scheme)
	WriteError(w, r, apperror.Wrap(cause, apperror.CodeUnauthorized, http.StatusUnauthorized, "token tidak valid atau tidak ada"))
}

// VisitorAuth verifies the visitor token from the rb_visitor cookie, falling back to the X-Visitor-Token
// header, and stores the Visitor in the request context. A missing or invalid token is 401 (R2.1).
func VisitorAuth(v TokenVerifier, now func() time.Time) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw := ""
			if c, err := r.Cookie(CookieVisitor); err == nil && c.Value != "" {
				raw = c.Value
			} else {
				raw = r.Header.Get(HeaderVisitorToken)
			}
			if raw == "" {
				unauthorized(w, r, `Bearer realm="rbt-visitor"`, token.ErrMalformed)
				return
			}
			claims, err := v.Verify(raw, now())
			if err != nil {
				unauthorized(w, r, `Bearer realm="rbt-visitor", error="invalid_token"`, err)
				return
			}
			vis := Visitor{ID: claims.Subject, BuyerNikHash: claims.NIKHash, Email: claims.Email}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), visitorKey{}, vis)))
		})
	}
}

// ServiceAuth guards /internal/v1/*. The Authorization bearer must verify against one of the verifiers
// (one per allowed calling service: its issuer and public key, audience = this service) and must live
// at most ServiceTokenMaxTTL, so a leaked token expires quickly. It fails closed: with no verifiers
// every request is rejected, never accepted.
func ServiceAuth(now func() time.Time, verifiers ...TokenVerifier) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, ok := BearerToken(r)
			if !ok {
				unauthorized(w, r, `Bearer realm="rbt-internal"`, token.ErrMalformed)
				return
			}
			lastErr := token.ErrSignature
			for _, v := range verifiers {
				claims, err := v.Verify(raw, now())
				if err != nil {
					lastErr = err
					continue
				}
				if claims.ExpiresAt-claims.IssuedAt > int64(ServiceTokenMaxTTL/time.Second) {
					lastErr = token.ErrClaims
					continue
				}
				caller := ServiceCaller{Issuer: claims.Issuer, Subject: claims.Subject}
				next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), serviceKey{}, caller)))
				return
			}
			unauthorized(w, r, `Bearer realm="rbt-internal", error="invalid_token"`, lastErr)
		})
	}
}
