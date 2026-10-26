package http

import (
	stdhttp "net/http"
	"strings"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/httpx"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
)

// InternalHandler serves the internal API called by the Queue Service (serviceBearer).
type InternalHandler struct {
	alloc        inbound.IAllocationUsecase
	supply       inbound.ISupplyUsecase
	payable      inbound.IGetPayableUsecase
	deadlineExt  inbound.IDeadlineExtensionUsecase
	paymentLock  inbound.IPaymentLockUsecase
	releaseAlloc inbound.IReleaseAllocationUsecase
	verifier     IUserTokenVerifier // service bearer verifier (EdDSA, ADR-011)
	paymentAuth  httpx.Middleware   // only the Payment Service token (payable, payment-lock); nil means refuse everything
}

// NewInternalHandler wires the handler.
func NewInternalHandler(alloc inbound.IAllocationUsecase, verifier IUserTokenVerifier) *InternalHandler {
	return &InternalHandler{alloc: alloc, verifier: verifier}
}

// WithSupply sets the supply usecase (builder pattern).
func (h *InternalHandler) WithSupply(s inbound.ISupplyUsecase) *InternalHandler {
	h.supply = s
	return h
}

// WithPayable sets the get-payable usecase (builder pattern).
func (h *InternalHandler) WithPayable(uc inbound.IGetPayableUsecase) *InternalHandler {
	h.payable = uc
	return h
}

// WithDeadlineExtension sets the deadline-extension usecase (builder pattern).
func (h *InternalHandler) WithDeadlineExtension(uc inbound.IDeadlineExtensionUsecase) *InternalHandler {
	h.deadlineExt = uc
	return h
}

// WithPaymentLock sets the payment-lock usecase (builder pattern).
func (h *InternalHandler) WithPaymentLock(uc inbound.IPaymentLockUsecase) *InternalHandler {
	h.paymentLock = uc
	return h
}

// WithReleaseAllocation sets the release-allocation usecase (builder pattern).
func (h *InternalHandler) WithReleaseAllocation(uc inbound.IReleaseAllocationUsecase) *InternalHandler {
	h.releaseAlloc = uc
	return h
}

// WithPaymentAuth sets the guard for the endpoints only the Payment Service calls (audit F2, payment Task 15.5).
// Use httpx.ServiceAuth with the Payment verifier. Without it those endpoints refuse every request.
func (h *InternalHandler) WithPaymentAuth(mw httpx.Middleware) *InternalHandler {
	h.paymentAuth = mw
	return h
}

// Register mounts the internal routes behind the service bearer.
func (h *InternalHandler) Register(mux *stdhttp.ServeMux) {
	if h.alloc != nil {
		mux.Handle("POST /internal/v1/allocations:batch", h.service(h.allocateBatch))
	}
	if h.supply != nil {
		mux.Handle("GET /internal/v1/events/{eventId}/supply", h.service(h.getSupply))
	}
	if h.payable != nil {
		mux.Handle("GET /internal/v1/bookings/{bookingId}/payable", h.paymentOnly(h.getPayable))
	}
	if h.deadlineExt != nil {
		mux.Handle("POST /internal/v1/bookings/{bookingId}/deadline-extension", h.service(h.extendDeadline))
	}
	if h.paymentLock != nil {
		mux.Handle("POST /internal/v1/bookings/{bookingId}/payment-lock", h.paymentOnly(h.lockPayment))
	}
	if h.releaseAlloc != nil {
		mux.Handle("POST /internal/v1/allocations/{rest}", h.service(h.releaseAllocation))
	}
}

// paymentOnly guards the endpoints that only the Payment Service may call: payable (reveals a buyer's booking) and
// payment-lock (locks a booking and fixes its amount). A user token or an admission token must never pass here, and the
// guard fails closed: with no Payment verifier configured every request is refused, never accepted.
func (h *InternalHandler) paymentOnly(next stdhttp.HandlerFunc) stdhttp.Handler {
	if h.paymentAuth == nil {
		return stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="rbt-internal"`)
			httpx.WriteError(w, r, inbound.ErrUnauthorized)
		})
	}
	return httpx.Chain(next, h.paymentAuth)
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

func (h *InternalHandler) getSupply(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	eventID := r.PathValue("eventId")
	snap, err := h.supply.GetSupply(r.Context(), eventID)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	noStore(w)
	httpx.WriteData(w, r, stdhttp.StatusOK, snap)
}

func (h *InternalHandler) getPayable(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	resp, err := h.payable.GetPayable(r.Context(), r.PathValue("bookingId"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	noStore(w)
	httpx.WriteData(w, r, stdhttp.StatusOK, resp)
}

func (h *InternalHandler) extendDeadline(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	var req dto.DeadlineExtensionRequest
	if err := httpx.DecodeJSON(w, r, &req, 0); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if err := h.deadlineExt.Extend(r.Context(), r.PathValue("bookingId"), req.Reason, req.Seconds); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	noStore(w)
	httpx.WriteData(w, r, stdhttp.StatusOK, struct{}{})
}

func (h *InternalHandler) lockPayment(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	var req dto.PaymentLockRequest
	if err := httpx.DecodeJSON(w, r, &req, 0); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	resp, err := h.paymentLock.Lock(r.Context(), r.PathValue("bookingId"), req.AttemptKey)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	noStore(w)
	httpx.WriteData(w, r, stdhttp.StatusOK, resp)
}

func (h *InternalHandler) releaseAllocation(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	var req dto.ReleaseRequest
	if err := httpx.DecodeJSON(w, r, &req, 0); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	// ServeMux wildcards must be a whole segment, so the contract path ".../{id}:release" arrives as one value.
	id, ok := strings.CutSuffix(r.PathValue("rest"), ":release")
	if !ok || id == "" {
		httpx.WriteError(w, r, inbound.ErrNotFound)
		return
	}
	resp, err := h.releaseAlloc.Release(r.Context(), id)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	noStore(w)
	httpx.WriteData(w, r, stdhttp.StatusOK, resp)
}
