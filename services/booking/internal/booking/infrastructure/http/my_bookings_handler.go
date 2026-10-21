package http

import (
	stdhttp "net/http"
	"strconv"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/httpx"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
)

// MyBookingsHandler serves the logged-in buyer's booking history.
type MyBookingsHandler struct {
	history  inbound.IBookingHistoryUsecase
	verifier IUserTokenVerifier
}

// NewMyBookingsHandler wires the handler.
func NewMyBookingsHandler(history inbound.IBookingHistoryUsecase, verifier IUserTokenVerifier) *MyBookingsHandler {
	return &MyBookingsHandler{history: history, verifier: verifier}
}

// Register mounts the route behind bearer auth.
func (h *MyBookingsHandler) Register(mux *stdhttp.ServeMux) {
	mux.Handle("GET /api/v1/me/bookings", withUser(h.verifier, h.listMyBookings))
}

func (h *MyBookingsHandler) listMyBookings(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	resp, err := h.history.ListMyBookings(r.Context(), accountIDFrom(r.Context()), r.URL.Query().Get("cursor"), limit)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	noStore(w)
	httpx.WriteData(w, r, stdhttp.StatusOK, resp)
}
