package http

import (
	stdhttp "net/http"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/httpx"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
)

// InternalHandler serves the internal API called by the Queue Service (serviceBearer).
type InternalHandler struct {
	alloc    inbound.IAllocationUsecase
	verifier IUserTokenVerifier // service bearer verifier (EdDSA, ADR-011)
}

// NewInternalHandler wires the handler.
func NewInternalHandler(alloc inbound.IAllocationUsecase, verifier IUserTokenVerifier) *InternalHandler {
	return &InternalHandler{alloc: alloc, verifier: verifier}
}

// Register mounts the internal routes behind the service bearer.
func (h *InternalHandler) Register(mux *stdhttp.ServeMux) {
	mux.Handle("POST /internal/v1/allocations:batch", h.service(h.allocateBatch))
}

// service guards internal endpoints with the service bearer token.
func (h *InternalHandler) service(next stdhttp.HandlerFunc) stdhttp.Handler {
	if h.verifier == nil {
		return next
	}
	return withUser(h.verifier, next)
}

func (h *InternalHandler) allocateBatch(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	var req dto.AllocationBatchRequest
	if err := httpx.DecodeJSON(w, r, &req, 1<<20); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	resp, err := h.alloc.AllocateBatch(r.Context(), req)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	noStore(w)
	httpx.WriteData(w, r, stdhttp.StatusOK, resp)
}
