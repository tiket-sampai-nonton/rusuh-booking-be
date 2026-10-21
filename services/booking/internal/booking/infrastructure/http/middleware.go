package http

import (
	"context"
	stdhttp "net/http"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/httpx"

	inbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
)

// VisitorID returns the visitor identity from the rb_visitor cookie (web), X-Visitor-ID header
// (raw UUID), or X-Visitor-Token header (mobile JWT — subject is the visitor id).
// It is empty when neither is present; usecases decide whether that is acceptable.
func VisitorID(r *stdhttp.Request) string {
	if c, err := r.Cookie("rb_visitor"); err == nil && c.Value != "" {
		return c.Value
	}
	if id := r.Header.Get("X-Visitor-ID"); id != "" {
		return id
	}
	return r.Header.Get("X-Visitor-Token")
}

type accountKey struct{}

func accountIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(accountKey{}).(string)
	return id
}

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
