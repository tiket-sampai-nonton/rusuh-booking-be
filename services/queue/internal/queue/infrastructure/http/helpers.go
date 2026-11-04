// Package http is the net/http adapter of the Queue domain. Each handler exposes Register(mux);
// the composition root only mounts them (design §1).
package http

import (
	stdhttp "net/http"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/apperror"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/httpx"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/inbound"
)

// cookieVisitor is the HttpOnly cookie that carries the visitor token set by join. It is only ever WRITTEN here: the
// visitor identity is read through VisitorAuthenticator, which verifies the token (an unverified id is never used).
const cookieVisitor = "rb_visitor"

// writeDomainError maps a domain error to the standard envelope, adding Retry-After for 429.
func writeDomainError(w stdhttp.ResponseWriter, r *stdhttp.Request, err error) {
	ae := inbound.MapError(err)
	if ae.Code == apperror.CodeRateLimited {
		if ra, ok := ae.Details["retryAfter"]; ok {
			w.Header().Set("Retry-After", ra)
		}
	}
	httpx.WriteError(w, r, ae)
}

// VisitorAuthenticator resolves and verifies the visitor identity for authenticated endpoints
// (me/claim/leave). It returns the visitor id encoded in the rb_visitor cookie or X-Visitor-Token.
type VisitorAuthenticator interface {
	Resolve(r *stdhttp.Request) (string, bool)
}
