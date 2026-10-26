package http

import (
	stdhttp "net/http"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/httpx"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/port/inbound"
)

// InternalHandler serves /internal/v1/payments/* for other services (internal-payment.yaml). Every route is
// behind httpx.ServiceAuth: only a service token from a known caller is accepted, never a visitor token.
type InternalHandler struct {
	resolve inbound.IResolvePayment
	auth    httpx.Middleware
}

// NewInternalHandler wires the handler; auth is httpx.ServiceAuth configured with the allowed callers.
func NewInternalHandler(resolve inbound.IResolvePayment, auth httpx.Middleware) *InternalHandler {
	return &InternalHandler{resolve: resolve, auth: auth}
}

// Register mounts the routes.
func (h *InternalHandler) Register(mux *stdhttp.ServeMux) {
	mux.Handle("POST /internal/v1/payments/{bookingId}/resolve", httpx.Chain(stdhttp.HandlerFunc(h.resolvePayment), h.auth))
}

func (h *InternalHandler) resolvePayment(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	var req dto.ResolveRequest
	if err := httpx.DecodeJSON(w, r, &req, 0); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	res, err := h.resolve.ResolvePayment(r.Context(), r.PathValue("bookingId"), req.Mode)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteData(w, r, stdhttp.StatusOK, res)
}
