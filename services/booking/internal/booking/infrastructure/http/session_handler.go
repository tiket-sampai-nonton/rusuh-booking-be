package http

import (
	stdhttp "net/http"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/httpx"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
	inbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
)

// SessionHandler serves booking-session and attendee-check endpoints.
type SessionHandler struct {
	sessions inbound.ISessionUsecase
	attendee inbound.IAttendeeCheckUsecase
	cancel   inbound.ICancelSessionUsecase
}

// NewSessionHandler wires the handler.
func NewSessionHandler(sessions inbound.ISessionUsecase, attendee inbound.IAttendeeCheckUsecase, cancel inbound.ICancelSessionUsecase) *SessionHandler {
	return &SessionHandler{sessions: sessions, attendee: attendee, cancel: cancel}
}

// Register mounts the session routes.
func (h *SessionHandler) Register(mux *stdhttp.ServeMux) {
	mux.HandleFunc("POST /api/v1/booking-sessions", h.createSession)
	mux.HandleFunc("GET /api/v1/booking-sessions/current", h.currentSession)
	mux.HandleFunc("GET /api/v1/booking-sessions/{sessionId}", h.getSession)
	mux.HandleFunc("POST /api/v1/booking-sessions/{sessionId}/attendee-checks", h.checkAttendee)
	mux.HandleFunc("POST /api/v1/booking-sessions/{sessionId}/cancel", h.cancelSession)
}

func (h *SessionHandler) createSession(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	var req dto.CreateSessionRequest
	if err := httpx.DecodeJSON(w, r, &req, 0); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	resp, err := h.sessions.CreateSession(r.Context(), req, VisitorID(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	noStore(w)
	httpx.WriteData(w, r, stdhttp.StatusCreated, resp)
}

func (h *SessionHandler) currentSession(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	eventID := r.URL.Query().Get("eventId")
	if eventID == "" {
		httpx.WriteError(w, r, inbound.Validation("eventId", "eventId wajib"))
		return
	}
	resp, err := h.sessions.GetCurrentSession(r.Context(), eventID, VisitorID(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	noStore(w)
	httpx.WriteData(w, r, stdhttp.StatusOK, resp)
}

func (h *SessionHandler) getSession(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	resp, err := h.sessions.GetSession(r.Context(), r.PathValue("sessionId"), VisitorID(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	noStore(w)
	httpx.WriteData(w, r, stdhttp.StatusOK, resp)
}

func (h *SessionHandler) cancelSession(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	if err := h.cancel.CancelSession(r.Context(), r.PathValue("sessionId"), VisitorID(r)); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.WriteHeader(stdhttp.StatusNoContent)
}

func (h *SessionHandler) checkAttendee(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	var req dto.AttendeeCheckRequest
	if err := httpx.DecodeJSON(w, r, &req, 0); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	resp, err := h.attendee.CheckAttendee(r.Context(), r.PathValue("sessionId"), req)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	noStore(w)
	httpx.WriteData(w, r, stdhttp.StatusOK, resp)
}
