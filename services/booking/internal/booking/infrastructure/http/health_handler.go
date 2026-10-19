// Package http contains the HTTP adapter (net/http) of the Booking domain.
package http

import (
	stdhttp "net/http"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/httpx"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
)

// HealthHandler serves GET /health (satu endpoint, dipakai health check ALB dan HEALTHCHECK container).
type HealthHandler struct {
	health inbound.IHealthUsecase
}

// NewHealthHandler creates the handler.
func NewHealthHandler(health inbound.IHealthUsecase) *HealthHandler {
	return &HealthHandler{health: health}
}

// Register mounts the routes (route registrar per domain, D-13).
func (h *HealthHandler) Register(mux *stdhttp.ServeMux) {
	mux.HandleFunc("GET /health", h.check)
}

func (h *HealthHandler) check(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	resp := h.health.CheckHealth(r.Context())
	status := stdhttp.StatusOK
	if resp.Status != dto.StatusUp {
		status = stdhttp.StatusServiceUnavailable
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, status, resp)
}
