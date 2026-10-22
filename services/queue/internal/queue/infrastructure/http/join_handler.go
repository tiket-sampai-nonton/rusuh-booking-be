package http

import (
	stdhttp "net/http"
	"strings"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/httpx"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/inbound"
)

// JoinHandler serves POST /api/v1/events/{eventId}/queue-entries (design §2.1).
type JoinHandler struct {
	uc         inbound.IJoinUsecase
	auth       VisitorAuthenticator // decodes rb_visitor JWT to get the visitor UUID
	trustProxy bool
	secure     bool // Secure flag on the visitor cookie (false in local http)
}

// NewJoinHandler creates the handler. trustProxy=true behind the ALB (X-Forwarded-For).
func NewJoinHandler(uc inbound.IJoinUsecase, trustProxy, secure bool) *JoinHandler {
	return &JoinHandler{uc: uc, trustProxy: trustProxy, secure: secure}
}

// WithAuth sets the visitor authenticator (call this to enable JWT decoding on join).
func (h *JoinHandler) WithAuth(auth VisitorAuthenticator) *JoinHandler {
	h.auth = auth
	return h
}

// Register mounts the route.
func (h *JoinHandler) Register(mux *stdhttp.ServeMux) {
	mux.HandleFunc("POST /api/v1/events/{eventId}/queue-entries", h.join)
}

func (h *JoinHandler) join(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	var body dto.JoinRequest
	if err := httpx.DecodeJSON(w, r, &body, 0); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	bearer, _ := httpx.BearerToken(r)
	// Resolve visitor ID: if a visitor JWT cookie is present and auth is set, decode it to get
	// the UUID subject (so the constraint key q:{e}:visitor:<uuid> is consistent with the first join).
	// Without this, the raw JWT is used as the visitor ID, breaking the one-active-per-visitor constraint.
	vid := visitorID(r)
	if vid != "" && h.auth != nil {
		if decoded, ok := h.auth.Resolve(r); ok {
			vid = decoded
		}
	}
	in := inbound.JoinInput{
		EventID:   r.PathValue("eventId"),
		Body:      body,
		Bearer:    bearer,
		VisitorID: vid,
		IsMobile:  isMobile(r),
		ClientIP:  httpx.ClientIP(r, h.trustProxy),
	}
	resp, err := h.uc.Join(r.Context(), in)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	// Web clients: move the freshly-minted visitor token into an HttpOnly cookie, hide it from the body.
	if resp.VisitorToken != "" && !in.IsMobile {
		stdhttp.SetCookie(w, &stdhttp.Cookie{
			Name: cookieVisitor, Value: resp.VisitorToken, Path: "/",
			HttpOnly: true, Secure: h.secure, SameSite: stdhttp.SameSiteLaxMode,
		})
		resp.VisitorToken = ""
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteData(w, r, stdhttp.StatusCreated, resp)
}

// isMobile reports whether the client is a native app (returns the visitor token in the body).
func isMobile(r *stdhttp.Request) bool {
	return strings.EqualFold(r.Header.Get("X-Client-Type"), "mobile")
}
