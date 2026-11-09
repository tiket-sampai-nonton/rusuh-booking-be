package http_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
	bookinghttp "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/infrastructure/http"
	inbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
)

type recordingEdit struct {
	cmd  *inbound.EditCommand
	resp dto.Booking
	err  error
}

func (r recordingEdit) ReplaceItems(_ context.Context, c inbound.EditCommand) (dto.Booking, error) {
	*r.cmd = c
	return r.resp, r.err
}

const keepBody = `{"version":1,"items":[{"categoryId":"c1","attendee":{"keepFromItemId":"aaaaaaaa-0000-0000-0000-00000000000a"}}]}`

// TC-EDT-07: keepFromItemId reaches the usecase untouched and the response carries only the masked NIK.
func TestBookingHandler_EditKeepFromItemID(t *testing.T) {
	var got inbound.EditCommand
	resp := dto.Booking{BookingID: "b1", Version: 2}
	h := bookinghttp.NewBookingHandler(&fakeHold{}, recordingEdit{cmd: &got, resp: resp}, fakeTickets{}, fakeGet{}, fakeCancel{})
	w := serve(t, h, http.MethodPut, "/api/v1/bookings/b1/items", keepBody, map[string]string{"X-Visitor-Token": "v-1"})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Len(t, got.Request.Items, 1)
	a := got.Request.Items[0].Attendee
	assert.Equal(t, "aaaaaaaa-0000-0000-0000-00000000000a", a.KeepFromItemID)
	assert.Empty(t, a.NIK)
	assert.Empty(t, a.FullName)
}

func TestBookingHandler_EditKeepErrorsMapToContract(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"invalid keep id", inbound.Validation("keepFromItemId", "keepFromItemId tidak valid"), http.StatusBadRequest, "VALIDATION_ERROR"},
		{"duplicate holder", inbound.ErrNikDuplicate, http.StatusConflict, "ATTENDEE_NIK_DUPLICATE_IN_ORDER"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got inbound.EditCommand
			h := bookinghttp.NewBookingHandler(&fakeHold{}, recordingEdit{cmd: &got, err: tc.err}, fakeTickets{}, fakeGet{}, fakeCancel{})
			w := serve(t, h, http.MethodPut, "/api/v1/bookings/b1/items", keepBody, nil)
			assert.Equal(t, tc.status, w.Code)
			var env struct {
				Meta struct {
					Message string `json:"message"`
				} `json:"meta"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &env))
			assert.True(t, strings.HasPrefix(env.Meta.Message, tc.code+": "), env.Meta.Message)
		})
	}
}
