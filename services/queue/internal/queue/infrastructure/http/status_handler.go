package http

import (
	stdhttp "net/http"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/httpx"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/inbound"
)

// StatusHandler serves GET /api/v1/queue-status/{eventId} (design §2.2). Cached by the CDN.
type StatusHandler struct {
	uc inbound.IStatusUsecase
}

// NewStatusHandler creates the handler.
func NewStatusHandler(uc inbound.IStatusUsecase) *StatusHandler {
	return &StatusHandler{uc: uc}
}

// Register mounts the route.
func (h *StatusHandler) Register(mux *stdhttp.ServeMux) {
	mux.HandleFunc("GET /api/v1/queue-status/{eventId}", h.status)
}

func (h *StatusHandler) status(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	doc, err := h.uc.GetStatus(r.Context(), r.PathValue("eventId"))
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=1, stale-while-revalidate=2")
	httpx.WriteData(w, r, stdhttp.StatusOK, doc)
}
