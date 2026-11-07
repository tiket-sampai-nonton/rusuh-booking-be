package http

import (
	stdhttp "net/http"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/httpx"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/inbound"
)

// HeartbeatHandler serves POST /api/v1/queue-entries/me/heartbeat?eventId= (ADR-030, R03). Visitor-authenticated;
// no body. Requests, responses and logs carry no PII, device input or raw token.
type HeartbeatHandler struct {
	uc   inbound.IHeartbeatUsecase
	auth VisitorTokenAuthenticator
}

// NewHeartbeatHandler creates the handler.
func NewHeartbeatHandler(uc inbound.IHeartbeatUsecase, auth VisitorTokenAuthenticator) *HeartbeatHandler {
	return &HeartbeatHandler{uc: uc, auth: auth}
}

// Register mounts the route.
func (h *HeartbeatHandler) Register(mux *stdhttp.ServeMux) {
	mux.HandleFunc("POST /api/v1/queue-entries/me/heartbeat", h.heartbeat)
}

func (h *HeartbeatHandler) heartbeat(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	c, ok := h.auth.ResolveToken(r)
	if !ok {
		writeDomainError(w, r, entity.ErrVisitorTokenInvalid)
		return
	}
	resp, err := h.uc.Heartbeat(r.Context(), inbound.HeartbeatInput{
		EventID: r.URL.Query().Get("eventId"), VisitorID: c.VisitorID, Gen: c.Gen,
	})
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteData(w, r, stdhttp.StatusOK, resp)
}
