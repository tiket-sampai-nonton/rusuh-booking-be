package http

import (
	"context"
	"errors"
	stdhttp "net/http"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/httpx"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/token"

	inbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
)

type visitorKey struct{}

// visitorScope is the scope Queue gives visitor tokens; admission, queue, and service tokens share the key
// but must not pass as a visitor.
const visitorScope = "visitor"

// VisitorID returns the verified visitor id (the visitor token's subject), or "" when the request carried no
// visitor token. It never trusts a raw header or cookie value: ResolveVisitor sets it after verifying the signature.
func VisitorID(r *stdhttp.Request) string {
	id, _ := r.Context().Value(visitorKey{}).(string)
	return id
}

// ResolveVisitor verifies the optional visitor token (rb_visitor cookie for web, X-Visitor-Token for mobile) and
// stores its subject for VisitorID. No token passes through (public routes, and session creation which is
// authorised by the admission token). A token that is present but invalid, expired, or not a visitor token is 401.
func ResolveVisitor(v httpx.TokenVerifier, now func() time.Time) httpx.Middleware {
	return func(next stdhttp.Handler) stdhttp.Handler {
		return stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
			raw := ""
			if c, err := r.Cookie(httpx.CookieVisitor); err == nil && c.Value != "" {
				raw = c.Value
			} else {
				raw = r.Header.Get(httpx.HeaderVisitorToken)
			}
			if raw == "" {
				next.ServeHTTP(w, r)
				return
			}
			var claims token.Claims
			err := errors.New("verifier tidak tersedia")
			if v != nil {
				claims, err = v.Verify(raw, now())
			}
			if err != nil || claims.Scope != visitorScope || claims.Subject == "" {
				w.Header().Set("WWW-Authenticate", `Bearer realm="rbt-visitor", error="invalid_token"`)
				httpx.WriteError(w, r, inbound.ErrUnauthorized)
				return
			}
			r2 := r.WithContext(context.WithValue(r.Context(), visitorKey{}, claims.Subject))
			next.ServeHTTP(w, r2)
			r.Pattern = r2.Pattern // AccessLog and metrics, outside this wrapper, read the matched route from the original request
		})
	}
}

type accountKey struct{}

func accountIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(accountKey{}).(string)
	return id
}

// denyAll rejects every request; guards use it when their verification key is not configured.
var denyAll = stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="rbt"`)
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
			w.Header().Set("WWW-Authenticate", `Bearer realm="rbt"`)
			httpx.WriteError(w, r, inbound.ErrUnauthorized)
			return
		}
		id, err := v.VerifyAccess(tok)
		if err != nil || id == "" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="rbt", error="invalid_token"`)
			httpx.WriteError(w, r, inbound.ErrUnauthorized)
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), accountKey{}, id)))
	})
}
