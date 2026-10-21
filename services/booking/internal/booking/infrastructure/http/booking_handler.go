package http

import (
	stdhttp "net/http"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/httpx"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
)

// BookingHandler serves hold, edit, and ticket endpoints.
type BookingHandler struct {
	hold    inbound.IHoldUsecase
	edit    inbound.IEditUsecase
	tickets inbound.ITicketUsecase
}

// NewBookingHandler wires the handler.
func NewBookingHandler(hold inbound.IHoldUsecase, edit inbound.IEditUsecase, tickets inbound.ITicketUsecase) *BookingHandler {
	return &BookingHandler{hold: hold, edit: edit, tickets: tickets}
}

// Register mounts the booking routes.
func (h *BookingHandler) Register(mux *stdhttp.ServeMux) {
	mux.HandleFunc("POST /api/v1/bookings", h.createBooking)
	mux.HandleFunc("PUT /api/v1/bookings/{bookingId}/items", h.replaceItems)
	mux.HandleFunc("GET /api/v1/bookings/{bookingId}/tickets", h.getTickets)
}

func (h *BookingHandler) createBooking(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	var req dto.HoldRequest
	if err := httpx.DecodeJSON(w, r, &req, 0); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	resp, err := h.hold.Hold(r.Context(), inbound.HoldCommand{
		Request:        req,
		VisitorID:      VisitorID(r),
		IdempotencyKey: r.Header.Get("Idempotency-Key"),
		CorrelationID:  httpx.RequestIDFrom(r.Context()),
	})
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	noStore(w)
	httpx.WriteData(w, r, stdhttp.StatusCreated, resp)
}

func (h *BookingHandler) replaceItems(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	var req dto.ReplaceItemsRequest
	if err := httpx.DecodeJSON(w, r, &req, 0); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	resp, err := h.edit.ReplaceItems(r.Context(), inbound.EditCommand{
		BookingID:     r.PathValue("bookingId"),
		Request:       req,
		VisitorID:     VisitorID(r),
		CorrelationID: httpx.RequestIDFrom(r.Context()),
	})
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	noStore(w)
	httpx.WriteData(w, r, stdhttp.StatusOK, resp)
}

func (h *BookingHandler) getTickets(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	resp, err := h.tickets.GetTickets(r.Context(), r.PathValue("bookingId"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	noStore(w)
	httpx.WriteData(w, r, stdhttp.StatusOK, resp)
}

func noStore(w stdhttp.ResponseWriter) { w.Header().Set("Cache-Control", "no-store") }
