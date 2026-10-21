package http

import (
	stdhttp "net/http"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/httpx"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/inbound"
)

// ClaimHandler serves POST /api/v1/queue-entries/{entryId}/claim (design §2.4). Visitor-authenticated.
type ClaimHandler struct {
	uc   inbound.IClaimUsecase
	auth VisitorAuthenticator
}

// NewClaimHandler creates the handler.
func NewClaimHandler(uc inbound.IClaimUsecase, auth VisitorAuthenticator) *ClaimHandler {
	return &ClaimHandler{uc: uc, auth: auth}
}

// Register mounts the route.
func (h *ClaimHandler) Register(mux *stdhttp.ServeMux) {
	mux.HandleFunc("POST /api/v1/queue-entries/{entryId}/claim", h.claim)
}

func (h *ClaimHandler) claim(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	vid, ok := h.auth.Resolve(r)
	if !ok {
		writeDomainError(w, r, entity.ErrNotFound)
		return
	}
	resp, err := h.uc.Claim(r.Context(), inbound.ClaimInput{
		EntryID:   r.PathValue("entryId"),
		VisitorID: vid,
		EventID:   r.URL.Query().Get("eventId"),
	})
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteData(w, r, stdhttp.StatusOK, resp)
}
