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

func TestGetPayable_ReturnsPayable(t *testing.T) {
	b := entity.Booking{
		ID: "bk-1", PublicRef: "RB-1", VisitorID: visitor,
		Status: vo.BookingPendingPayment, AmountMinor: 1000, Version: 1,
		PaymentExpiresAt: time.Now().Add(time.Hour),
	}
	uc := usecase.NewGetPayable(&fakeBookingRepo{booking: b}, fixedClock{t: time.Now()})
	resp, err := uc.GetPayable(context.Background(), "bk-1")
	require.NoError(t, err)
	assert.Equal(t, "bk-1", resp.BookingID)
	assert.Equal(t, "RB-1", resp.PublicRef)
}

func TestGetPayable_NotPayable_PaidStatus(t *testing.T) {
	b := entity.Booking{ID: "bk-1", Status: vo.BookingPaid, PaymentExpiresAt: time.Now().Add(time.Hour)}
	uc := usecase.NewGetPayable(&fakeBookingRepo{booking: b}, fixedClock{t: time.Now()})
	_, err := uc.GetPayable(context.Background(), "bk-1")
	assert.ErrorIs(t, err, inbound.ErrNotPayable)
}

func TestGetPayable_Expired(t *testing.T) {
	b := entity.Booking{ID: "bk-1", Status: vo.BookingPendingPayment, PaymentExpiresAt: time.Now().Add(-time.Minute)}
	uc := usecase.NewGetPayable(&fakeBookingRepo{booking: b}, fixedClock{t: time.Now()})
	_, err := uc.GetPayable(context.Background(), "bk-1")
	assert.ErrorIs(t, err, inbound.ErrPaymentExpired)
}

func TestGetPayable_NotFound(t *testing.T) {
	uc := usecase.NewGetPayable(&fakeBookingRepo{getErr: inbound.ErrNotFound}, fixedClock{t: time.Now()})
	_, err := uc.GetPayable(context.Background(), "bk-1")
	assert.ErrorIs(t, err, inbound.ErrNotFound)
}
