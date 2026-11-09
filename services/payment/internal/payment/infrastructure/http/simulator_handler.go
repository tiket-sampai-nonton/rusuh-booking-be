package http

import (
	stdhttp "net/http"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/httpx"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/port/inbound"
)

// SimulatorHandler serves the local payment simulator. It is only constructed and registered when APP_ENV is
// local or ci (delivery/payment_module.go); in every other environment the route does not exist.
type SimulatorHandler struct {
	uc   inbound.ISimulatePayment
	auth *PayerAuth
}

// NewSimulatorHandler wires the handler.
func NewSimulatorHandler(uc inbound.ISimulatePayment, auth *PayerAuth) *SimulatorHandler {
	return &SimulatorHandler{uc: uc, auth: auth}
}

// Register mounts the route. Only the visitor token is accepted: the simulator is a developer tool for the
// owner of the attempt, never reachable through a WebView page session.
func (h *SimulatorHandler) Register(mux *stdhttp.ServeMux) {
	mux.Handle("POST /api/v1/local-simulator/payments/{attemptId}/scenario",
		httpx.Chain(stdhttp.HandlerFunc(h.scenario), h.auth.VisitorOnly()))
}

func (h *SimulatorHandler) scenario(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	var req dto.SimulatorRequest
	if err := httpx.DecodeJSON(w, r, &req, 0); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	res, err := h.uc.Simulate(r.Context(), payer(r), r.PathValue("attemptId"), req.Scenario)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	noStore(w)
	httpx.WriteData(w, r, stdhttp.StatusAccepted, res)
}
