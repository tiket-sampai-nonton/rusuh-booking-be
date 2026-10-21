package http

import (
	stdhttp "net/http"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/inbound"
)

// LeaveHandler serves DELETE /api/v1/queue-entries/{entryId} (design §2.5). Visitor-authenticated.
type LeaveHandler struct {
	uc   inbound.ILeaveUsecase
	auth VisitorAuthenticator
}

// NewLeaveHandler creates the handler.
func NewLeaveHandler(uc inbound.ILeaveUsecase, auth VisitorAuthenticator) *LeaveHandler {
	return &LeaveHandler{uc: uc, auth: auth}
}

// Register mounts the route.
func (h *LeaveHandler) Register(mux *stdhttp.ServeMux) {
	mux.HandleFunc("DELETE /api/v1/queue-entries/{entryId}", h.leave)
}

func (h *LeaveHandler) leave(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	vid, ok := h.auth.Resolve(r)
	if !ok {
		writeDomainError(w, r, entity.ErrNotFound)
		return
	}
	err := h.uc.Leave(r.Context(), inbound.LeaveInput{
		EntryID:   r.PathValue("entryId"),
		VisitorID: vid,
		EventID:   r.URL.Query().Get("eventId"),
	})
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	w.WriteHeader(stdhttp.StatusNoContent)
}
