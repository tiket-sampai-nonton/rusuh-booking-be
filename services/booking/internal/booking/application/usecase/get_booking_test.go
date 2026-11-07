package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/apperror"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/application/usecase"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/entity"
	vo "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/valueobject"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
)

func TestGetBooking_ReturnsBooking(t *testing.T) {
	b := entity.Booking{ID: bookingID, PublicRef: "RB-1", VisitorID: visitor,
		Status: vo.BookingPendingPayment, AmountMinor: 1000, Currency: "IDR", Version: 1,
		PaymentExpiresAt: time.Now().Add(time.Hour)}
	uc := usecase.NewGetBooking(&fakeBookingRepo{booking: b}, fixedClock{t: time.Now()})
	resp, err := uc.GetBooking(context.Background(), bookingID, visitor)
	require.NoError(t, err)
	assert.Equal(t, bookingID, resp.BookingID)
	assert.Equal(t, "RB-1", resp.PublicRef)
}

func TestGetBooking_NotFound(t *testing.T) {
	uc := usecase.NewGetBooking(&fakeBookingRepo{getErr: inbound.ErrNotFound}, fixedClock{t: time.Now()})
	_, err := uc.GetBooking(context.Background(), bookingID, visitor)
	assert.ErrorIs(t, err, inbound.ErrNotFound)
}

func TestGetBooking_VisitorMismatch(t *testing.T) {
	b := entity.Booking{ID: bookingID, VisitorID: "other-visitor", Status: vo.BookingPendingPayment}
	uc := usecase.NewGetBooking(&fakeBookingRepo{booking: b}, fixedClock{t: time.Now()})
	_, err := uc.GetBooking(context.Background(), bookingID, visitor)
	assert.ErrorIs(t, err, inbound.ErrVisitorMismatch)
}

// REQ-4: a malformed bookingId is rejected with a field validation error BEFORE the repo is touched
// (→ 400, not 404); a well-formed-but-absent UUID still reaches the repo and surfaces 404.
func TestGetBooking_PathUUIDValidation(t *testing.T) {
	tests := []struct {
		name      string
		bookingID string
		wantField string // non-empty = expect VALIDATION_ERROR on this field, repo untouched
		repoErr   error  // used only on the valid-uuid row
		wantErrIs error
	}{
		{name: "non-uuid rejected before repo", bookingID: "not-a-uuid", wantField: "bookingId"},
		{name: "valid-but-absent uuid reaches repo and 404s", bookingID: bookingID, repoErr: inbound.ErrNotFound, wantErrIs: inbound.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeBookingRepo{getErr: tt.repoErr}
			uc := usecase.NewGetBooking(repo, fixedClock{t: time.Now()})
			_, err := uc.GetBooking(context.Background(), tt.bookingID, visitor)
			require.Error(t, err)
			if tt.wantField != "" {
				assert.Equal(t, apperror.CodeValidationFailed, codeOf(t, err))
				assert.Equal(t, tt.wantField, validationField(t, err))
				assert.Zero(t, repo.calls, "repo must not be called for a malformed id")
				return
			}
			assert.ErrorIs(t, err, tt.wantErrIs)
			assert.Positive(t, repo.calls, "valid uuid must reach the repo")
		})
	}
}
