package http

import (
	stdhttp "net/http"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/inbound"
)

// LeaveHandler serves DELETE /api/v1/queue-entries/{entryId} (design §2.5). Visitor-authenticated.
type LeaveHandler struct {
	uc   inbound.ILeaveUsecase
	auth VisitorTokenAuthenticator
}

// NewLeaveHandler creates the handler.
func NewLeaveHandler(uc inbound.ILeaveUsecase, auth VisitorTokenAuthenticator) *LeaveHandler {
	return &LeaveHandler{uc: uc, auth: auth}
}

// Register mounts the route.
func (h *LeaveHandler) Register(mux *stdhttp.ServeMux) {
	mux.HandleFunc("DELETE /api/v1/queue-entries/{entryId}", h.leave)
}

func (h *LeaveHandler) leave(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	c, ok := h.auth.ResolveToken(r)
	if !ok {
		writeDomainError(w, r, entity.ErrVisitorTokenInvalid)
		return
	}
	eventID, ok := queryEventID(w, r)
	if !ok {
		return
	}
	err := h.uc.Leave(r.Context(), inbound.LeaveInput{
		EntryID:   r.PathValue("entryId"),
		VisitorID: c.VisitorID,
		Gen:       c.Gen,
		EventID:   eventID,
	})
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	w.WriteHeader(stdhttp.StatusNoContent)
}
