// Package http is the net/http adapter of the Queue domain. Each handler exposes Register(mux);
// the composition root only mounts them (design §1).
package http

import (
	stdhttp "net/http"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/apperror"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/httpx"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/valueobject"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
	shared "github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/shared/entity"
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

// queryEventID returns the eventId query value of a visitor route (claim, leave, me). A missing, empty or non-UUID value
// is answered 400 VALIDATION_ERROR before the usecase runs (D-C6.7); the caller has already passed the token check.
func queryEventID(w stdhttp.ResponseWriter, r *stdhttp.Request) (string, bool) {
	raw := r.URL.Query().Get("eventId")
	if _, err := valueobject.NewEventID(raw); err != nil {
		msg := "harus berupa UUID"
		if raw == "" {
			msg = "wajib diisi"
		}
		writeDomainError(w, r, shared.ValidationErrors{{Field: "eventId", Message: msg}})
		return "", false
	}
	return raw, true
}

// VisitorAuthenticator resolves and verifies the visitor identity for authenticated endpoints
// (me/claim/leave). It returns the visitor id encoded in the rb_visitor cookie or X-Visitor-Token.
type VisitorAuthenticator interface {
	Resolve(r *stdhttp.Request) (string, bool)
}

// VisitorTokenAuthenticator also exposes the token generation and the device claim (ADR-030): join, heartbeat and me
// compare the generation with the server record and bind tokens to the X-Device-Id hash. GuestScope is the
// idempotency scope of a tokenless join (C6.2).
type VisitorTokenAuthenticator interface {
	VisitorAuthenticator
	ResolveToken(r *stdhttp.Request) (outbound.VisitorClaim, bool)
	DeviceHash(r *stdhttp.Request) string
	GuestScope(r *stdhttp.Request, clientIP string) string
}
