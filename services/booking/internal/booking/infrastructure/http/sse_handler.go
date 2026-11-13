package http

import (
	stdhttp "net/http"
	"regexp"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/httpx"
	inbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
)

// uuidRe matches a canonical UUID. SSE commits its 200 status before the usecase runs, so the handler must
// reject a malformed path id up front (REQ-4) — the usecase guard still exists and is the authoritative check
// for every non-SSE caller.
var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

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
	sessionID := r.PathValue("sessionId")
	// Validate the path id BEFORE committing the SSE 200 headers: a malformed sessionId is a 400, and once
	// the stream headers are flushed the status can no longer change (REQ-4). The usecase repeats this check
	// as its own guard for completeness and unit coverage.
	if !uuidRe.MatchString(sessionID) {
		httpx.WriteError(w, r, inbound.Validation("sessionId", "sessionId harus UUID"))
		return
	}
	// SSE needs a flushable writer; without one (e.g. a buffering proxy test) report 503.
	flusher, ok := w.(stdhttp.Flusher)
	if !ok {
		httpx.WriteError(w, r, inbound.ErrDependency)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	// The server's WriteTimeout is set per response and would cut a stream that is meant to outlive it, so clear the
	// deadline for this response only (C7.5). A writer without deadline support returns an error, which is ignored.
	_ = stdhttp.NewResponseController(w).SetWriteDeadline(time.Time{})
	w.WriteHeader(stdhttp.StatusOK)
	flusher.Flush() // commit the headers before the first frame
	// Once headers are sent we cannot change the status, so stream errors just end the stream.
	_ = h.stream.Stream(r.Context(), sessionID, VisitorID(r), w, flusher.Flush)
}
