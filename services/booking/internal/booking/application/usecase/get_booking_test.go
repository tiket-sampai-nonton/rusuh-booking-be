package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/application/usecase"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/entity"
	vo "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/valueobject"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
)

func TestGetBooking_ReturnsBooking(t *testing.T) {
	b := entity.Booking{ID: "bk-1", PublicRef: "RB-1", VisitorID: visitor,
		Status: vo.BookingPendingPayment, AmountMinor: 1000, Currency: "IDR", Version: 1,
		PaymentExpiresAt: time.Now().Add(time.Hour)}
	uc := usecase.NewGetBooking(&fakeBookingRepo{booking: b}, fixedClock{t: time.Now()})
	resp, err := uc.GetBooking(context.Background(), "bk-1", visitor)
	require.NoError(t, err)
	assert.Equal(t, "bk-1", resp.BookingID)
	assert.Equal(t, "RB-1", resp.PublicRef)
}

func TestGetBooking_NotFound(t *testing.T) {
	uc := usecase.NewGetBooking(&fakeBookingRepo{getErr: inbound.ErrNotFound}, fixedClock{t: time.Now()})
	_, err := uc.GetBooking(context.Background(), "bk-1", visitor)
	assert.ErrorIs(t, err, inbound.ErrNotFound)
}

func TestGetBooking_VisitorMismatch(t *testing.T) {
	b := entity.Booking{ID: "bk-1", VisitorID: "other-visitor", Status: vo.BookingPendingPayment}
	uc := usecase.NewGetBooking(&fakeBookingRepo{booking: b}, fixedClock{t: time.Now()})
	_, err := uc.GetBooking(context.Background(), "bk-1", visitor)
	assert.ErrorIs(t, err, inbound.ErrVisitorMismatch)
}
