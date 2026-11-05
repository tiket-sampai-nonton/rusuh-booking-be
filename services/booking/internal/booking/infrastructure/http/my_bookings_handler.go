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
	limit, err := parseHistoryLimit(r.URL.Query().Get("limit"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	resp, err := h.history.ListMyBookings(r.Context(), accountIDFrom(r.Context()), r.URL.Query().Get("cursor"), limit)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	noStore(w)
	httpx.WriteData(w, r, stdhttp.StatusOK, resp)
}

// parseHistoryLimit validates the optional `limit` query param against the contract
// (integer, minimum 1, maximum 50; GET /api/v1/me/bookings). An absent/empty value
// returns 0 so the usecase applies its default; a non-integer or out-of-range value
// is rejected with 400 VALIDATION_ERROR rather than being silently clamped.
func parseHistoryLimit(raw string) (int, error) {
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, inbound.Validation("limit", "limit harus berupa bilangan bulat 1..50")
	}
	if n < 1 || n > 50 {
		return 0, inbound.Validation("limit", "limit harus di antara 1 dan 50")
	}
	return n, nil
}
