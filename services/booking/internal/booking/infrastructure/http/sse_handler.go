package http

import (
	stdhttp "net/http"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/httpx"
	inbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
)

// SSEHandler serves the live availability + timer stream over Server-Sent Events.
type SSEHandler struct{ stream inbound.IStreamSessionUsecase }

// NewSSEHandler wires the SSE handler.
func NewSSEHandler(stream inbound.IStreamSessionUsecase) *SSEHandler {
	return &SSEHandler{stream: stream}
}

// Register mounts the SSE route.
func (h *SSEHandler) Register(mux *stdhttp.ServeMux) {
	mux.HandleFunc("GET /api/v1/booking-sessions/{sessionId}/stream", h.streamSession)
}

func (h *SSEHandler) streamSession(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	// SSE needs a flushable writer; without one (e.g. a buffering proxy test) report 503.
	flusher, ok := w.(stdhttp.Flusher)
	if !ok {
		httpx.WriteError(w, r, inbound.ErrDependency)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(stdhttp.StatusOK)
	flusher.Flush() // commit the headers before the first frame
	sessionID := r.PathValue("sessionId")
	// Once headers are sent we cannot change the status, so stream errors just end the stream.
	_ = h.stream.Stream(r.Context(), sessionID, VisitorID(r), w, flusher.Flush)
}
