package http_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
	bookinghttp "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/infrastructure/http"
	inbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
)

// --- fakes for the session / admin / history / allocation / edit use cases ---

type fakeSessions struct {
	sess dto.BookingSession
	err  error
	gotV string
}

func (f *fakeSessions) CreateSession(_ context.Context, _ dto.CreateSessionRequest, v string) (dto.BookingSession, error) {
	f.gotV = v
	return f.sess, f.err
}
func (f *fakeSessions) GetSession(_ context.Context, _, v string) (dto.BookingSession, error) {
	f.gotV = v
	return f.sess, f.err
}
func (f *fakeSessions) GetCurrentSession(_ context.Context, _, v string) (dto.BookingSession, error) {
	f.gotV = v
	return f.sess, f.err
}

type fakeAttendee struct {
	res dto.AttendeeCheck
	err error
}

func (f fakeAttendee) CheckAttendee(context.Context, string, dto.AttendeeCheckRequest) (dto.AttendeeCheck, error) {
	return f.res, f.err
}

type fakeCancelSession struct {
	err error
	got string
}

func (f *fakeCancelSession) CancelSession(_ context.Context, id, v string) error {
	f.got = id + "/" + v
	return f.err
}

type fakeAdminUC struct {
	id     string
	err    error
	gotCfg dto.UpdateEventConfigRequest
}

func (f *fakeAdminUC) Create(context.Context, dto.CreateEventRequest) (string, error) {
	return f.id, f.err
}
func (f *fakeAdminUC) Update(_ context.Context, _ string, r dto.UpdateEventConfigRequest) error {
	f.gotCfg = r
	return f.err
}

type fakeHistory struct {
	got  string
	resp dto.MyBookings
	err  error
}

func (f *fakeHistory) ListMyBookings(_ context.Context, account, cursor string, limit int) (dto.MyBookings, error) {
	f.got = account + "|" + cursor
	return f.resp, f.err
}

type fakeAlloc struct {
	resp dto.AllocationBatchResponse
	err  error
}

func (f fakeAlloc) AllocateBatch(context.Context, dto.AllocationBatchRequest) (dto.AllocationBatchResponse, error) {
	return f.resp, f.err
}

type fakeDeadline struct {
	reason string
	secs   int
	err    error
}

func (f *fakeDeadline) Extend(_ context.Context, _ string, reason string, secs int) error {
	f.reason, f.secs = reason, secs
	return f.err
}

type recEdit struct {
	resp dto.Booking
	err  error
	got  inbound.EditCommand
}

func (f *recEdit) ReplaceItems(_ context.Context, c inbound.EditCommand) (dto.Booking, error) {
	f.got = c
	return f.resp, f.err
}

func do(t *testing.T, reg interface{ Register(*http.ServeMux) }, method, target, body string, headers map[string]string) *httptest.ResponseRecorder {
	return serve(t, reg, method, target, body, headers)
}

func TestSessionHandler_Routes(t *testing.T) {
	sess := &fakeSessions{sess: dto.BookingSession{SessionID: "s1", EventID: "e1", Phase: "SELECTING"}}
	cancel := &fakeCancelSession{}
	h := bookinghttp.NewSessionHandler(sess, fakeAttendee{res: dto.AttendeeCheck{Result: "AVAILABLE", ChecksRemaining: 9}}, cancel)
	vh := map[string]string{"X-Visitor-Token": "v-1"}

	w := do(t, h, http.MethodPost, "/api/v1/booking-sessions", `{"admissionToken":"tok"}`, vh)
	assert.Equal(t, http.StatusCreated, w.Code)
	assert.Equal(t, "v-1", sess.gotV, "visitor id comes from the verified token")
	assert.Equal(t, "no-store", w.Header().Get("Cache-Control"))

	assert.Equal(t, http.StatusOK, do(t, h, http.MethodGet, "/api/v1/booking-sessions/s1", "", vh).Code)
	assert.Equal(t, http.StatusOK, do(t, h, http.MethodGet, "/api/v1/booking-sessions/current?eventId=e1", "", vh).Code)
	assert.Equal(t, http.StatusBadRequest, do(t, h, http.MethodGet, "/api/v1/booking-sessions/current", "", vh).Code, "eventId wajib")

	w = do(t, h, http.MethodPost, "/api/v1/booking-sessions/s1/attendee-checks", `{"nik":"3174011501900001"}`, vh)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"checksRemaining":9`)

	w = do(t, h, http.MethodPost, "/api/v1/booking-sessions/s1/cancel", "", vh)
	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Equal(t, "s1/v-1", cancel.got)

	assert.Equal(t, http.StatusBadRequest, do(t, h, http.MethodPost, "/api/v1/booking-sessions", `{bukan json`, vh).Code)
}

func TestSessionHandler_ErrorsKeepTheirStatus(t *testing.T) {
	h := bookinghttp.NewSessionHandler(&fakeSessions{err: inbound.ErrVisitorMismatch}, fakeAttendee{err: inbound.ErrNotFound}, &fakeCancelSession{err: inbound.ErrNotFound})
	assert.Equal(t, http.StatusForbidden, do(t, h, http.MethodGet, "/api/v1/booking-sessions/s1", "", nil).Code)
	assert.Equal(t, http.StatusNotFound, do(t, h, http.MethodPost, "/api/v1/booking-sessions/s1/attendee-checks", `{"nik":"x"}`, nil).Code)
	assert.Equal(t, http.StatusNotFound, do(t, h, http.MethodPost, "/api/v1/booking-sessions/s1/cancel", "", nil).Code)
}

func TestAdminHandler_CreateAndUpdate(t *testing.T) {
	uc := &fakeAdminUC{id: "ev-1"}
	h := bookinghttp.NewAdminHandler(uc, uc, allowAll{})
	auth := map[string]string{"Authorization": "Bearer t"}

	w := do(t, h, http.MethodPost, "/internal/v1/admin/events", `{"name":"x","slug":"x"}`, auth)
	assert.Equal(t, http.StatusCreated, w.Code)
	assert.Contains(t, w.Body.String(), `"eventId":"ev-1"`)

	w = do(t, h, http.MethodPatch, "/internal/v1/admin/events/ev-1", `{"paymentTtlSeconds":900}`, auth)
	assert.Equal(t, http.StatusOK, w.Code)

	assert.Equal(t, http.StatusUnauthorized, do(t, h, http.MethodPost, "/internal/v1/admin/events", `{}`, nil).Code)
	assert.Equal(t, http.StatusBadRequest, do(t, h, http.MethodPost, "/internal/v1/admin/events", `{bukan json`, auth).Code)

	bad := &fakeAdminUC{err: inbound.ErrRulesLocked}
	hb := bookinghttp.NewAdminHandler(bad, bad, allowAll{})
	assert.Equal(t, http.StatusConflict, do(t, hb, http.MethodPatch, "/internal/v1/admin/events/ev-1", `{"maxEdits":1}`, auth).Code)
	assert.Equal(t, http.StatusConflict, do(t, hb, http.MethodPost, "/internal/v1/admin/events", `{"name":"x"}`, auth).Code)
}

func TestAdminHandler_FailsClosedWithoutAVerifier(t *testing.T) {
	uc := &fakeAdminUC{id: "ev-1"}
	h := bookinghttp.NewAdminHandler(uc, uc, nil)
	assert.Equal(t, http.StatusUnauthorized, do(t, h, http.MethodPost, "/internal/v1/admin/events", `{}`, map[string]string{"Authorization": "Bearer t"}).Code)
}

func TestMyBookingsHandler_UsesTheVerifiedAccount(t *testing.T) {
	hist := &fakeHistory{resp: dto.MyBookings{Bookings: []dto.MyBookingItem{{BookingID: "b1"}}}}
	h := bookinghttp.NewMyBookingsHandler(hist, allowAll{})
	w := do(t, h, http.MethodGet, "/api/v1/me/bookings?cursor=c1&limit=5", "", map[string]string{"Authorization": "Bearer t"})
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "queue-service|c1", hist.got, "akun diambil dari token terverifikasi, bukan dari query")
	assert.Equal(t, http.StatusUnauthorized, do(t, h, http.MethodGet, "/api/v1/me/bookings", "", nil).Code)

	bad := bookinghttp.NewMyBookingsHandler(&fakeHistory{err: inbound.ErrDependency}, allowAll{})
	assert.Equal(t, http.StatusServiceUnavailable, do(t, bad, http.MethodGet, "/api/v1/me/bookings", "", map[string]string{"Authorization": "Bearer t"}).Code)
}

func TestInternalHandler_AllocateBatchAndDeadline(t *testing.T) {
	dl := &fakeDeadline{}
	h := bookinghttp.NewInternalHandler(fakeAlloc{resp: dto.AllocationBatchResponse{Results: []dto.AllocationResult{{QueueEntryID: "q1", Outcome: "GRANTED"}}}}, allowAll{}).WithDeadlineExtension(dl)
	auth := map[string]string{"Authorization": "Bearer t"}

	w := do(t, h, http.MethodPost, "/internal/v1/allocations:batch", `{"eventId":"e1","entries":[]}`, auth)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"outcome":"GRANTED"`)
	assert.Equal(t, http.StatusBadRequest, do(t, h, http.MethodPost, "/internal/v1/allocations:batch", `{bukan json`, auth).Code)
	assert.Equal(t, http.StatusUnauthorized, do(t, h, http.MethodPost, "/internal/v1/allocations:batch", `{}`, nil).Code)

	w = do(t, h, http.MethodPost, "/internal/v1/bookings/b1/deadline-extension", `{"reason":"PAYMENT_PENDING","seconds":120}`, auth)
	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, "PAYMENT_PENDING", dl.reason)
	assert.Equal(t, 120, dl.secs)

	bad := bookinghttp.NewInternalHandler(fakeAlloc{err: inbound.ErrDependency}, allowAll{})
	assert.Equal(t, http.StatusServiceUnavailable, do(t, bad, http.MethodPost, "/internal/v1/allocations:batch", `{"eventId":"e1"}`, auth).Code)
}

func TestBookingHandler_ReplaceItemsPassesTheVerifiedVisitor(t *testing.T) {
	edit := &recEdit{resp: dto.Booking{BookingID: "b1", Version: 3}}
	h := bookinghttp.NewBookingHandler(&fakeHold{}, edit, fakeTickets{}, fakeGet{}, fakeCancel{})
	w := do(t, h, http.MethodPut, "/api/v1/bookings/b1/items", `{"version":2,"items":[]}`, map[string]string{"X-Visitor-Token": "v-9"})
	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, "v-9", edit.got.VisitorID)
	assert.Equal(t, "b1", edit.got.BookingID)
	assert.Equal(t, 2, edit.got.Request.Version)

	bad := bookinghttp.NewBookingHandler(&fakeHold{}, &recEdit{err: inbound.ErrVersionConflict}, fakeTickets{}, fakeGet{}, fakeCancel{})
	assert.Equal(t, http.StatusConflict, do(t, bad, http.MethodPut, "/api/v1/bookings/b1/items", `{"version":1,"items":[]}`, nil).Code)
	assert.Equal(t, http.StatusBadRequest, do(t, h, http.MethodPut, "/api/v1/bookings/b1/items", `{bukan`, nil).Code)
}

// Who is asking is decided here (verified visitor token, verified buyer bearer, email link); the use case decides ownership.
func TestBookingHandler_GetTicketsPassesVerifiedIdentity(t *testing.T) {
	var got inbound.TicketAccess
	h := bookinghttp.NewBookingHandler(&fakeHold{}, &recEdit{}, fakeTickets{resp: dto.TicketList{BookingID: "b1"}, got: &got}, fakeGet{}, fakeCancel{}).WithBuyerAuth(allowAll{})

	w := do(t, h, http.MethodGet, "/api/v1/bookings/b1/tickets?t=link-1", "", map[string]string{"X-Visitor-Token": "v-1"})
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, inbound.TicketAccess{VisitorID: "v-1", LinkToken: "link-1"}, got)

	w = do(t, h, http.MethodGet, "/api/v1/bookings/b1/tickets", "", map[string]string{"Authorization": "Bearer t"})
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "queue-service", got.AccountID, "akun dari bearer terverifikasi")

	assert.Equal(t, http.StatusUnauthorized, do(t, bookinghttp.NewBookingHandler(&fakeHold{}, &recEdit{}, fakeTickets{}, fakeGet{}, fakeCancel{}),
		http.MethodGet, "/api/v1/bookings/b1/tickets", "", map[string]string{"Authorization": "Bearer t"}).Code, "bearer tanpa verifier ditolak")
	denied := bookinghttp.NewBookingHandler(&fakeHold{}, &recEdit{}, fakeTickets{}, fakeGet{}, fakeCancel{}).WithBuyerAuth(denyAllVerifier{})
	assert.Equal(t, http.StatusUnauthorized, do(t, denied, http.MethodGet, "/api/v1/bookings/b1/tickets", "", map[string]string{"Authorization": "Bearer t"}).Code)

	notFound := bookinghttp.NewBookingHandler(&fakeHold{}, &recEdit{}, fakeTickets{err: inbound.ErrNotFound}, fakeGet{}, fakeCancel{})
	assert.Equal(t, http.StatusNotFound, do(t, notFound, http.MethodGet, "/api/v1/bookings/b1/tickets", "", nil).Code)
}
