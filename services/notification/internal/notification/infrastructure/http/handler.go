// Package http contains the HTTP adapter (net/http) of the Notification domain.
package http

import (
	stdhttp "net/http"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/httpx"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/port/inbound"
)

// Handler exposes the Notification Service over HTTP.
type Handler struct {
	service   string
	readiness inbound.IReadinessUsecase
}

// NewHandler creates the HTTP handler.
func NewHandler(service string, readiness inbound.IReadinessUsecase) *Handler {
	return &Handler{service: service, readiness: readiness}
}

// Routes registers all routes. Endpoint bisnis ditambahkan setelah kontrak API final (docs/contracts).
func (h *Handler) Routes() stdhttp.Handler {
	mux := stdhttp.NewServeMux()
	mux.HandleFunc("GET /healthz", h.healthz)
	mux.HandleFunc("GET /readyz", h.readyz)
	return mux
}

func (h *Handler) healthz(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
	httpx.WriteJSON(w, stdhttp.StatusOK, map[string]string{"status": "ok", "service": h.service})
}

func (h *Handler) readyz(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	resp, err := h.readiness.CheckReadiness(r.Context())
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	status := stdhttp.StatusOK
	if resp.Status != dto.StatusReady {
		status = stdhttp.StatusServiceUnavailable
	}
	httpx.WriteJSON(w, status, resp)
}
