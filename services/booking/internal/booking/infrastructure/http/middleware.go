package http

import (
	"context"
	"errors"
	stdhttp "net/http"
	"strings"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/httpx"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/token"

	inbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
)

type visitorKey struct{}

// visitorScope is the scope Queue gives visitor tokens; admission, queue, and service tokens share the key
// but must not pass as a visitor.
const visitorScope = "visitor"

// headerWWWAuthenticate is the challenge header every 401 in this file sets (RFC 6750).
const headerWWWAuthenticate = "WWW-Authenticate"

// VisitorID returns the verified visitor id (the visitor token's subject), or "" when the request carried no
// visitor token. It never trusts a raw header or cookie value: ResolveVisitor sets it after verifying the signature.
func VisitorID(r *stdhttp.Request) string {
	id, _ := r.Context().Value(visitorKey{}).(string)
	return id
}

// ResolveVisitor verifies the optional visitor token (rb_visitor cookie for web, X-Visitor-Token for mobile) and
// stores its subject for VisitorID. No token passes through (public routes, and session creation which is
// authorised by the admission token). A token that is present but invalid, expired, or not a visitor token is 401,
// except on the public catalog reads (isPublicCatalogRead), which never use the identity: a stale cookie must not blank
// the catalog. An EXPIRED rb_visitor cookie is also cleared in that 401 so the browser self-heals; any other failure
// (bad signature, wrong scope) may be Booking's own key being wrong or rotating, and clearing then would wipe every
// visitor's queue identity, so the cookie is kept.
func ResolveVisitor(v httpx.TokenVerifier, now func() time.Time) httpx.Middleware {
	return func(next stdhttp.Handler) stdhttp.Handler {
		return stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
			raw, fromCookie := visitorToken(r)
			if raw == "" || isPublicCatalogRead(r) {
				next.ServeHTTP(w, r)
				return
			}
			subject, err := verifyVisitor(v, raw, now())
			if err != nil {
				rejectVisitor(w, r, err, fromCookie)
				return
			}
			r2 := r.WithContext(context.WithValue(r.Context(), visitorKey{}, subject))
			next.ServeHTTP(w, r2)
			r.Pattern = r2.Pattern // AccessLog and metrics, outside this wrapper, read the matched route from the original request
		})
	}
}

// visitorToken returns the raw visitor token: the rb_visitor cookie (web) first, else X-Visitor-Token (mobile).
func visitorToken(r *stdhttp.Request) (raw string, fromCookie bool) {
	if c, err := r.Cookie(httpx.CookieVisitor); err == nil && c.Value != "" {
		return c.Value, true
	}
	return r.Header.Get(httpx.HeaderVisitorToken), false
}

var (
	errNoVerifier = errors.New("verifier tidak tersedia")
	errNotVisitor = errors.New("token bukan token visitor")
)

// verifyVisitor returns the subject of a valid visitor-scope token. Without a verifier every token fails (fail-closed).
func verifyVisitor(v httpx.TokenVerifier, raw string, at time.Time) (string, error) {
	if v == nil {
		return "", errNoVerifier
	}
	claims, err := v.Verify(raw, at)
	if err != nil {
		return "", err
	}
	if claims.Scope != visitorScope || claims.Subject == "" {
		return "", errNotVisitor
	}
	return claims.Subject, nil
}

// rejectVisitor answers 401. Only an expired cookie is cleared (see ResolveVisitor).
func rejectVisitor(w stdhttp.ResponseWriter, r *stdhttp.Request, err error, fromCookie bool) {
	if fromCookie && errors.Is(err, token.ErrExpired) {
		expireVisitorCookie(w)
	}
	w.Header().Set(headerWWWAuthenticate, `Bearer realm="rbt-visitor", error="invalid_token"`)
	httpx.WriteError(w, r, inbound.ErrUnauthorized)
}

// isPublicCatalogRead reports the exact public catalog reads: GET /api/v1/events, /events/facets, /events/{eventId}.
// Anything deeper (e.g. /events/{id}/queue-entries) is not exempt.
func isPublicCatalogRead(r *stdhttp.Request) bool {
	rest, ok := strings.CutPrefix(r.URL.Path, "/api/v1/events")
	if r.Method != stdhttp.MethodGet || !ok || (rest != "" && rest[0] != '/') {
		return false
	}
	return !strings.Contains(strings.TrimPrefix(rest, "/"), "/")
}

// expireVisitorCookie deletes rb_visitor. Name, Path and host-only Domain match the cookie Queue sets at join, which is
// what the browser matches on. SameSite=None (with Secure) is required for the deletion to be accepted on a cross-site
// response (web app on another site than the API); it carries no CSRF risk because the cookie is empty and expired.
func expireVisitorCookie(w stdhttp.ResponseWriter) {
	stdhttp.SetCookie(w, &stdhttp.Cookie{ //nolint:gosec // G124: SameSite=None disengaja, cookie kosong dan kedaluwarsa (lihat komentar)
		Name: httpx.CookieVisitor, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: true, SameSite: stdhttp.SameSiteNoneMode,
	})
}

type accountKey struct{}

func accountIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(accountKey{}).(string)
	return id
}

// denyAll rejects every request; guards use it when their verification key is not configured.
var denyAll = stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	w.Header().Set(headerWWWAuthenticate, `Bearer realm="rbt"`)
	httpx.WriteError(w, r, inbound.ErrUnauthorized)
})

// IUserTokenVerifier verifies a User Service access token and returns the account id (sub).
type IUserTokenVerifier interface {
	VerifyAccess(token string) (accountID string, err error)
}

// withUser verifies the bearer token and stores the account id in context, or returns 401.
func withUser(v IUserTokenVerifier, next stdhttp.HandlerFunc) stdhttp.Handler {
	return stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		tok, ok := httpx.BearerToken(r)
		if !ok {
			w.Header().Set(headerWWWAuthenticate, `Bearer realm="rbt"`)
			httpx.WriteError(w, r, inbound.ErrUnauthorized)
			return
		}
		id, err := v.VerifyAccess(tok)
		if err != nil || id == "" {
			w.Header().Set(headerWWWAuthenticate, `Bearer realm="rbt", error="invalid_token"`)
			httpx.WriteError(w, r, inbound.ErrUnauthorized)
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), accountKey{}, id)))
	})
}
