package http_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/httpx"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
	bookinghttp "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/infrastructure/http"
	inbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
)

// --- fakes ---

type fakeCatalog struct {
	list   dto.EventList
	detail dto.EventDetail
	err    error
}

func (f fakeCatalog) ListEvents(context.Context, dto.EventListQuery) (dto.EventList, error) {
	return f.list, f.err
}
func (f fakeCatalog) GetEventDetail(context.Context, string) (dto.EventDetail, error) {
	return f.detail, f.err
}

type fakeHold struct {
	resp dto.Booking
	err  error
	cmd  inbound.HoldCommand
}

func (f *fakeHold) Hold(_ context.Context, cmd inbound.HoldCommand) (dto.Booking, error) {
	f.cmd = cmd
	return f.resp, f.err
}

type fakeEdit struct {
	resp dto.Booking
	err  error
}

func (f fakeEdit) ReplaceItems(context.Context, inbound.EditCommand) (dto.Booking, error) {
	return f.resp, f.err
}

type fakeTickets struct {
	resp dto.TicketList
	err  error
}

func (f fakeTickets) GetTickets(context.Context, string) (dto.TicketList, error) {
	return f.resp, f.err
}

type fakeGet struct {
	resp dto.Booking
	err  error
}

func (f fakeGet) GetBooking(context.Context, string, string) (dto.Booking, error) {
	return f.resp, f.err
}

type fakeCancel struct {
	resp dto.Booking
	err  error
}

func (f fakeCancel) Cancel(context.Context, string, string) (dto.Booking, error) {
	return f.resp, f.err
}

func serve(t *testing.T, reg interface{ Register(*http.ServeMux) }, method, target string, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	reg.Register(mux)
	h := httpx.Chain(mux, httpx.RequestID())
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, target, nil)
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestCatalogHandler_ListAndDetail(t *testing.T) {
	cat := fakeCatalog{list: dto.EventList{Events: []dto.EventSummary{{EventID: "e1"}}}}
	h := bookinghttp.NewCatalogHandler(cat, cat)

	w := serve(t, h, http.MethodGet, "/api/v1/events?sort=PRICE_ASC", "", nil)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "public, max-age=2", w.Header().Get("Cache-Control"))

	var env struct {
		Data dto.EventList `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &env))
	assert.Len(t, env.Data.Events, 1)
}

func TestCatalogHandler_DetailNotFound(t *testing.T) {
	h := bookinghttp.NewCatalogHandler(fakeCatalog{err: inbound.ErrNotFound}, fakeCatalog{err: inbound.ErrNotFound})
	w := serve(t, h, http.MethodGet, "/api/v1/events/22222222-2222-2222-2222-222222222222", "", nil)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestBookingHandler_CreatePassesIdempotencyKey(t *testing.T) {
	hold := &fakeHold{resp: dto.Booking{BookingID: "bk-1"}}
	h := bookinghttp.NewBookingHandler(hold, fakeEdit{}, fakeTickets{}, fakeGet{}, fakeCancel{})
	w := serve(t, h, http.MethodPost, "/api/v1/bookings",
		`{"sessionId":"s","items":[]}`,
		map[string]string{"Idempotency-Key": "abc", "X-Visitor-Token": "v-1"})
	assert.Equal(t, http.StatusCreated, w.Code)
	assert.Equal(t, "abc", hold.cmd.IdempotencyKey)
	assert.Equal(t, "v-1", hold.cmd.VisitorID)
}

func TestBookingHandler_HoldError(t *testing.T) {
	h := bookinghttp.NewBookingHandler(&fakeHold{err: inbound.ErrCategoryUnavailable}, fakeEdit{}, fakeTickets{}, fakeGet{}, fakeCancel{})
	w := serve(t, h, http.MethodPost, "/api/v1/bookings", `{"sessionId":"s","items":[]}`, map[string]string{"Idempotency-Key": "abc"})
	assert.Equal(t, http.StatusConflict, w.Code)

	var env struct {
		Meta struct {
			Message string `json:"message"`
		} `json:"meta"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &env))
	assert.True(t, strings.HasPrefix(env.Meta.Message, "CATEGORY_UNAVAILABLE: "), env.Meta.Message)
}

func TestBookingHandler_GetTicketsNotPayable(t *testing.T) {
	h := bookinghttp.NewBookingHandler(&fakeHold{}, fakeEdit{}, fakeTickets{err: inbound.ErrNotPayable}, fakeGet{}, fakeCancel{})
	w := serve(t, h, http.MethodGet, "/api/v1/bookings/66666666-6666-6666-6666-666666666666/tickets", "", nil)
	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestBookingHandler_GetBookingOK(t *testing.T) {
	h := bookinghttp.NewBookingHandler(&fakeHold{}, fakeEdit{}, fakeTickets{},
		fakeGet{resp: dto.Booking{BookingID: "bk-1", PublicRef: "RB-1"}}, fakeCancel{})
	w := serve(t, h, http.MethodGet, "/api/v1/bookings/bk-1", "", map[string]string{"X-Visitor-Token": "v-1"})
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "no-store", w.Header().Get("Cache-Control"))

	var env struct {
		Data dto.Booking `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &env))
	assert.Equal(t, "bk-1", env.Data.BookingID)
	assert.Equal(t, "RB-1", env.Data.PublicRef)
}

func TestBookingHandler_GetBookingVisitorMismatch(t *testing.T) {
	h := bookinghttp.NewBookingHandler(&fakeHold{}, fakeEdit{}, fakeTickets{},
		fakeGet{err: inbound.ErrVisitorMismatch}, fakeCancel{})
	w := serve(t, h, http.MethodGet, "/api/v1/bookings/bk-1", "", map[string]string{"X-Visitor-Token": "other"})
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestBookingHandler_GetBookingNotFound(t *testing.T) {
	h := bookinghttp.NewBookingHandler(&fakeHold{}, fakeEdit{}, fakeTickets{},
		fakeGet{err: inbound.ErrNotFound}, fakeCancel{})
	w := serve(t, h, http.MethodGet, "/api/v1/bookings/bk-1", "", map[string]string{"X-Visitor-Token": "v-1"})
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestBookingHandler_CancelOK(t *testing.T) {
	h := bookinghttp.NewBookingHandler(&fakeHold{}, fakeEdit{}, fakeTickets{}, fakeGet{},
		fakeCancel{resp: dto.Booking{BookingID: "bk-1", Status: "CANCELLED"}})
	w := serve(t, h, http.MethodPost, "/api/v1/bookings/bk-1/cancel", "", map[string]string{"X-Visitor-Token": "v-1"})
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "no-store", w.Header().Get("Cache-Control"))

	var env struct {
		Data dto.Booking `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &env))
	assert.Equal(t, "CANCELLED", env.Data.Status)
}

func TestBookingHandler_CancelLocked(t *testing.T) {
	h := bookinghttp.NewBookingHandler(&fakeHold{}, fakeEdit{}, fakeTickets{}, fakeGet{},
		fakeCancel{err: inbound.ErrBookingLocked})
	w := serve(t, h, http.MethodPost, "/api/v1/bookings/bk-1/cancel", "", map[string]string{"X-Visitor-Token": "v-1"})
	assert.Equal(t, http.StatusConflict, w.Code)
}
