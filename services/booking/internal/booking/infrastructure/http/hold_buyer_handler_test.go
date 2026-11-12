package http_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
	bookinghttp "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/infrastructure/http"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
)

type fakeUserVerifier struct{ id string }

func (f fakeUserVerifier) VerifyAccess(tok string) (string, error) {
	if tok != "good" {
		return "", inbound.ErrUnauthorized
	}
	return f.id, nil
}

const holdBody = `{"sessionId":"s","buyer":{"nik":"3174010101010009","email":"b@example.com","emailConfirmation":"b@example.com","consent":true},"items":[]}`

func TestBookingHandler_HoldDecodesBuyerBlock(t *testing.T) {
	hold := &fakeHold{resp: dto.Booking{BookingID: "bk-1"}}
	h := bookinghttp.NewBookingHandler(hold, fakeEdit{}, fakeTickets{}, fakeGet{}, fakeCancel{})
	w := serve(t, h, http.MethodPost, "/api/v1/bookings", holdBody, map[string]string{"Idempotency-Key": "abc", "X-Visitor-Token": "v-1"})
	require.Equal(t, http.StatusCreated, w.Code)
	assert.Equal(t, "3174010101010009", hold.cmd.Request.Buyer.NIK)
	assert.True(t, hold.cmd.Request.Buyer.Consent)
	assert.Empty(t, hold.cmd.AccountID, "no bearer: guest")
}

func TestBookingHandler_HoldBearerSetsAccountOnly(t *testing.T) {
	hold := &fakeHold{resp: dto.Booking{BookingID: "bk-1"}}
	h := bookinghttp.NewBookingHandler(hold, fakeEdit{}, fakeTickets{}, fakeGet{}, fakeCancel{}).WithBuyerAuth(fakeUserVerifier{id: "acc-1"})
	w := serve(t, h, http.MethodPost, "/api/v1/bookings", holdBody,
		map[string]string{"Idempotency-Key": "abc", "X-Visitor-Token": "v-1", "Authorization": "Bearer good"})
	require.Equal(t, http.StatusCreated, w.Code)
	assert.Equal(t, "acc-1", hold.cmd.AccountID)
	assert.Equal(t, "3174010101010009", hold.cmd.Request.Buyer.NIK, "the buyer NIK still comes from the body")

	w = serve(t, h, http.MethodPost, "/api/v1/bookings", holdBody,
		map[string]string{"Idempotency-Key": "abc", "X-Visitor-Token": "v-1", "Authorization": "Bearer bad"})
	assert.Equal(t, http.StatusUnauthorized, w.Code, "a presented but invalid bearer is rejected")
}

func TestBookingHandler_HoldRefusalMapping(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		status     int
		code       string
		retryAfter string
	}{
		{"nik at cap", inbound.ErrBuyerNikLimitReached, http.StatusUnprocessableEntity, "BUYER_NIK_LIMIT_REACHED", ""},
		{"throttled", inbound.BuyerNikThrottled(7), http.StatusTooManyRequests, "BUYER_NIK_THROTTLED", "7"},
		{"consent", inbound.ErrConsentRequired, http.StatusUnprocessableEntity, "CONSENT_REQUIRED", ""},
		{"quantity", inbound.ErrQuantityInvalid, http.StatusUnprocessableEntity, "QUANTITY_INVALID", ""},
		{"email mismatch", inbound.ErrEmailMismatch, http.StatusUnprocessableEntity, "EMAIL_MISMATCH", ""},
		{"category ids", inbound.CategoryUnavailable([]string{"b", "a"}), http.StatusConflict, "CATEGORY_UNAVAILABLE", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := bookinghttp.NewBookingHandler(&fakeHold{err: tt.err}, fakeEdit{}, fakeTickets{}, fakeGet{}, fakeCancel{})
			w := serve(t, h, http.MethodPost, "/api/v1/bookings", holdBody, map[string]string{"Idempotency-Key": "abc"})
			assert.Equal(t, tt.status, w.Code)
			assert.Equal(t, tt.retryAfter, w.Header().Get("Retry-After"))
			var env struct {
				Meta struct {
					Code    int    `json:"code"`
					Message string `json:"message"`
				} `json:"meta"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &env))
			assert.Equal(t, tt.status, env.Meta.Code)
			assert.True(t, strings.HasPrefix(env.Meta.Message, tt.code+": "), env.Meta.Message)
			assert.NotContains(t, w.Body.String(), "3174010101010009", "no buyer NIK echoed")
			if tt.code == "CATEGORY_UNAVAILABLE" {
				assert.Contains(t, env.Meta.Message, "CATEGORY_UNAVAILABLE: a,b", "sorted ids, comma separated")
			}
		})
	}
}

type fakeNIKVerifier struct{ fakeUserVerifier }

func (f fakeNIKVerifier) VerifyAccessFull(tok string) (string, []byte, string, error) {
	id, err := f.VerifyAccess(tok)
	if err != nil {
		return "", nil, "", err
	}
	return id, []byte("login-hash"), "3174********0001", nil
}

func TestBookingHandler_HoldAttendeeOnlyUseLoginNIK(t *testing.T) {
	const body = `{"sessionId":"s","buyer":{"nik":"3174010101010009","email":"b@example.test","emailConfirmation":"b@example.test","consent":true},` +
		`"items":[{"categoryId":"c","attendee":{"useLoginNik":true,"fullName":"Alice"}}]}`
	hold := &fakeHold{resp: dto.Booking{BookingID: "bk-1"}}
	h := bookinghttp.NewBookingHandler(hold, fakeEdit{}, fakeTickets{}, fakeGet{}, fakeCancel{}).
		WithBuyerAuth(fakeNIKVerifier{fakeUserVerifier{id: "acc-1"}})

	w := serve(t, h, http.MethodPost, "/api/v1/bookings", body,
		map[string]string{"Idempotency-Key": "abc", "X-Visitor-Token": "v-1", "Authorization": "Bearer good"})
	require.Equal(t, http.StatusCreated, w.Code)
	assert.Equal(t, []byte("login-hash"), hold.cmd.LoginNIKHash)
	assert.Equal(t, "3174010101010009", hold.cmd.Request.Buyer.NIK, "buyer NIK tetap dari body")
	assert.Equal(t, "good", hold.cmd.BearerToken)

	w = serve(t, h, http.MethodPost, "/api/v1/bookings", body, map[string]string{"Idempotency-Key": "abc", "X-Visitor-Token": "v-1"})
	assert.Equal(t, http.StatusUnauthorized, w.Code, "attendee useLoginNik tanpa bearer ditolak")
}

func TestBookingHandler_HoldBuyerUseLoginNIK_Refusals(t *testing.T) {
	const body = `{"sessionId":"s","buyer":{"useLoginNik":true,"email":"b@example.test","emailConfirmation":"b@example.test","consent":true},"items":[]}`
	h := map[string]string{"Idempotency-Key": "abc", "X-Visitor-Token": "v-1"}
	bearer := map[string]string{"Idempotency-Key": "abc", "X-Visitor-Token": "v-1", "Authorization": "Bearer good"}
	badBearer := map[string]string{"Idempotency-Key": "abc", "X-Visitor-Token": "v-1", "Authorization": "Bearer bad"}
	newH := func(v bookinghttp.IUserTokenVerifier) *bookinghttp.BookingHandler {
		return bookinghttp.NewBookingHandler(&fakeHold{}, fakeEdit{}, fakeTickets{}, fakeGet{}, fakeCancel{}).WithBuyerAuth(v)
	}

	assert.Equal(t, http.StatusUnauthorized, serve(t, newH(fakeNIKVerifier{fakeUserVerifier{id: "acc-1"}}), http.MethodPost, "/api/v1/bookings", body, h).Code, "tanpa bearer")
	assert.Equal(t, http.StatusUnauthorized, serve(t, newH(fakeUserVerifier{id: "acc-1"}), http.MethodPost, "/api/v1/bookings", body, bearer).Code, "verifier tanpa klaim NIK")
	assert.Equal(t, http.StatusUnauthorized, serve(t, newH(fakeNIKVerifier{fakeUserVerifier{id: "acc-1"}}), http.MethodPost, "/api/v1/bookings", body, badBearer).Code, "token ditolak")
}
