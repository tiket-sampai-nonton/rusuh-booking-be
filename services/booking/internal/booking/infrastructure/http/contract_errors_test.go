package http_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	bookinghttp "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/infrastructure/http"
	inbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
)

// codeOf reads the errorCode prefix of the standard error envelope ("CODE: message").
func codeOf(t *testing.T, body []byte) string {
	t.Helper()
	var env struct {
		Meta struct {
			Message string `json:"message"`
		} `json:"meta"`
	}
	require.NoError(t, json.Unmarshal(body, &env), string(body))
	code, _, _ := strings.Cut(env.Meta.Message, ": ")
	return code
}

// TC-API-03 (SRS-IF-30/33): every error response documented in booking.yaml / internal-booking.yaml is one the
// handlers really return, with the documented status and errorCode.
func TestContract_BookingResponsesAreReallyReturned(t *testing.T) {
	vh := map[string]string{"X-Visitor-Token": "v-1"}
	sessions := func(err error) *bookinghttp.SessionHandler {
		return bookinghttp.NewSessionHandler(&fakeSessions{err: err}, fakeAttendee{}, &fakeCancelSession{err: err})
	}
	tests := []struct {
		name   string
		call   func() *httptest.ResponseRecorder
		status int
		code   string
	}{
		{"3.1 GET current without eventId", func() *httptest.ResponseRecorder {
			return do(t, sessions(nil), http.MethodGet, "/api/v1/booking-sessions/current", "", vh)
		}, 400, "VALIDATION_ERROR"},
		{"3.1 GET current with a non-UUID eventId", func() *httptest.ResponseRecorder {
			return do(t, sessions(inbound.Validation("eventId", "eventId harus UUID")), http.MethodGet, "/api/v1/booking-sessions/current?eventId=x", "", vh)
		}, 400, "VALIDATION_ERROR"},
		{"3.2 POST booking-sessions with malformed JSON", func() *httptest.ResponseRecorder {
			return do(t, sessions(nil), http.MethodPost, "/api/v1/booking-sessions", `{bukan json`, vh)
		}, 400, "VALIDATION_ERROR"},
		{"3.2 POST booking-sessions with an unknown field", func() *httptest.ResponseRecorder {
			return do(t, sessions(nil), http.MethodPost, "/api/v1/booking-sessions", `{"admissionToken":"t","x":1}`, vh)
		}, 400, "VALIDATION_ERROR"},
		{"3.2 POST booking-sessions without admissionToken", func() *httptest.ResponseRecorder {
			return do(t, sessions(inbound.Validation("admissionToken", "admissionToken wajib")), http.MethodPost, "/api/v1/booking-sessions", `{}`, vh)
		}, 400, "VALIDATION_ERROR"},
		{"3.3 POST bookings/{id}/cancel while Payment is unclear", func() *httptest.ResponseRecorder {
			h := bookinghttp.NewBookingHandler(&fakeHold{}, fakeEdit{}, fakeTickets{}, fakeGet{}, fakeCancel{err: inbound.ErrDependency})
			return do(t, h, http.MethodPost, "/api/v1/bookings/b1/cancel", "", vh)
		}, 503, "DEPENDENCY_UNAVAILABLE"},
		{"3.6 POST booking-sessions/{id}/cancel with a hold", func() *httptest.ResponseRecorder {
			return do(t, sessions(inbound.ErrBookingLocked), http.MethodPost, "/api/v1/booking-sessions/s1/cancel", "", vh)
		}, 409, "BOOKING_LOCKED"},
		{"3.7 PUT items keep id not usable", func() *httptest.ResponseRecorder {
			h := bookinghttp.NewBookingHandler(&fakeHold{}, fakeEdit{err: inbound.Validation("keepFromItemId", "keepFromItemId tidak valid")}, fakeTickets{}, fakeGet{}, fakeCancel{})
			return do(t, h, http.MethodPut, "/api/v1/bookings/b1/items", `{"version":1,"items":[]}`, vh)
		}, 400, "VALIDATION_ERROR"},
		{"3.7 PUT items duplicate holder", func() *httptest.ResponseRecorder {
			h := bookinghttp.NewBookingHandler(&fakeHold{}, fakeEdit{err: inbound.ErrNikDuplicate}, fakeTickets{}, fakeGet{}, fakeCancel{})
			return do(t, h, http.MethodPut, "/api/v1/bookings/b1/items", `{"version":1,"items":[]}`, vh)
		}, 409, "ATTENDEE_NIK_DUPLICATE_IN_ORDER"},
		{"3.5 GET internal supply of an unknown event", func() *httptest.ResponseRecorder {
			h := bookinghttp.NewInternalHandler(nil, allowAll{}).WithSupply(&stubSupply{err: inbound.ErrNotFound})
			return do(t, h, http.MethodGet, "/internal/v1/events/unknown/supply", "", map[string]string{"Authorization": "Bearer t"})
		}, 404, "NOT_FOUND"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := tc.call()
			assert.Equal(t, tc.status, w.Code, w.Body.String())
			assert.Equal(t, tc.code, codeOf(t, w.Body.Bytes()))
		})
	}
}
