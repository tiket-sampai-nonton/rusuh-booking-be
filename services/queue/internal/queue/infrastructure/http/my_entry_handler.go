package http

import (
	stdhttp "net/http"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/httpx"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/inbound"
)

// MyEntryHandler serves GET /api/v1/queue-entries/me?eventId= (design §2.3). Visitor-authenticated.
type MyEntryHandler struct {
	uc   inbound.IMyEntryUsecase
	auth VisitorTokenAuthenticator
}

// NewMyEntryHandler creates the handler.
func NewMyEntryHandler(uc inbound.IMyEntryUsecase, auth VisitorTokenAuthenticator) *MyEntryHandler {
	return &MyEntryHandler{uc: uc, auth: auth}
}

// Register mounts the route.
func (h *MyEntryHandler) Register(mux *stdhttp.ServeMux) {
	mux.HandleFunc("GET /api/v1/queue-entries/me", h.me)
}

func (h *MyEntryHandler) me(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	c, ok := h.auth.ResolveToken(r)
	if !ok {
		writeDomainError(w, r, entity.ErrVisitorTokenInvalid) // R01.6: invalid/expired/device mismatch -> 401
		return
	}
	eventID, ok := queryEventID(w, r)
	if !ok {
		return
	}
	resp, err := h.uc.GetMyEntry(r.Context(), inbound.MyEntryInput{
		EventID:   eventID,
		VisitorID: c.VisitorID,
		Gen:       c.Gen,
	})
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteData(w, r, stdhttp.StatusOK, resp)
}
