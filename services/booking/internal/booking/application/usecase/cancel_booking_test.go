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
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

func TestCancelBooking_CancelsWhenNotPaid(t *testing.T) {
	b := entity.Booking{
		ID: "bk-1", VisitorID: visitor, Status: vo.BookingPendingPayment,
		PaymentExpiresAt: time.Now().Add(time.Hour),
	}
	uow := &fakeUoW{}
	payments := &fakePayments{outcome: outbound.OutcomeNotPaid}
	uc := usecase.NewCancelBooking(&fakeBookingRepo{booking: b}, payments, uow, fixedClock{t: time.Now()})
	_, err := uc.Cancel(context.Background(), "bk-1", visitor)
	require.NoError(t, err)
	assert.Equal(t, "CANCELLED", uow.expireSpec.Reason)
	assert.Equal(t, []outbound.ResolveMode{outbound.ResolveCheckAndCancel}, payments.modes)
}

func TestCancelBooking_LockedAfterPaymentStarted(t *testing.T) {
	started := time.Now()
	b := entity.Booking{
		ID: "bk-1", VisitorID: visitor, Status: vo.BookingVerifying,
		PaymentStartedAt: &started, PaymentExpiresAt: time.Now().Add(time.Hour),
	}
	uc := usecase.NewCancelBooking(&fakeBookingRepo{booking: b}, &fakePayments{}, &fakeUoW{}, fixedClock{t: time.Now()})
	_, err := uc.Cancel(context.Background(), "bk-1", visitor)
	assert.ErrorIs(t, err, inbound.ErrBookingLocked)
}

func TestCancelBooking_VisitorMismatch(t *testing.T) {
	b := entity.Booking{ID: "bk-1", VisitorID: "other", Status: vo.BookingPendingPayment}
	uc := usecase.NewCancelBooking(&fakeBookingRepo{booking: b}, &fakePayments{}, &fakeUoW{}, fixedClock{t: time.Now()})
	_, err := uc.Cancel(context.Background(), "bk-1", visitor)
	assert.ErrorIs(t, err, inbound.ErrVisitorMismatch)
}

func TestCancelBooking_NotFound(t *testing.T) {
	uc := usecase.NewCancelBooking(&fakeBookingRepo{getErr: inbound.ErrNotFound}, &fakePayments{}, &fakeUoW{}, fixedClock{t: time.Now()})
	_, err := uc.Cancel(context.Background(), "bk-1", visitor)
	assert.ErrorIs(t, err, inbound.ErrNotFound)
}

func TestCancelBooking_AlreadyTerminal_IsNoOp(t *testing.T) {
	b := entity.Booking{ID: "bk-1", VisitorID: visitor, Status: vo.BookingPaid}
	uow := &fakeUoW{}
	uc := usecase.NewCancelBooking(&fakeBookingRepo{booking: b}, &fakePayments{}, uow, fixedClock{t: time.Now()})
	resp, err := uc.Cancel(context.Background(), "bk-1", visitor)
	require.NoError(t, err)
	assert.Equal(t, string(vo.BookingPaid), resp.Status)
	assert.False(t, uow.expireActed, "no mutation for terminal booking")
}

func TestCancelBooking_RaceCondition_PaymentFoundSettles(t *testing.T) {
	b := entity.Booking{
		ID: "bk-1", VisitorID: visitor, Status: vo.BookingPendingPayment,
		PaymentExpiresAt: time.Now().Add(time.Hour),
	}
	payments := &fakePayments{outcome: outbound.OutcomeSucceeded}
	uow := &fakeUoW{settled: true}
	uc := usecase.NewCancelBooking(&fakeBookingRepo{booking: b}, payments, uow, fixedClock{t: time.Now()})
	_, err := uc.Cancel(context.Background(), "bk-1", visitor)
	require.NoError(t, err)
	assert.False(t, uow.expireActed, "should not expire when payment found")
}

// Stock must never be released before the payment status is certain (invariant #7).
func TestCancelBooking_UnknownPaymentStatusRefusesAndChangesNothing(t *testing.T) {
	b := entity.Booking{ID: "bk-1", VisitorID: visitor, Status: vo.BookingPendingPayment, PaymentExpiresAt: time.Now().Add(time.Hour)}
	for name, p := range map[string]*fakePayments{
		"UNKNOWN":      {outcome: outbound.OutcomeUnknown},
		"Payment mati": {err: inbound.ErrDependency},
	} {
		t.Run(name, func(t *testing.T) {
			uow := &fakeUoW{}
			uc := usecase.NewCancelBooking(&fakeBookingRepo{booking: b}, p, uow, fixedClock{t: time.Now()})
			_, err := uc.Cancel(context.Background(), "bk-1", visitor)
			assert.ErrorIs(t, err, inbound.ErrDependency)
			assert.False(t, uow.expireActed)
			assert.Empty(t, uow.settleSpec.BookingID)
		})
	}
}

func TestCancelBooking_NoAttemptCancels(t *testing.T) {
	b := entity.Booking{ID: "bk-1", VisitorID: visitor, Status: vo.BookingPendingPayment, PaymentExpiresAt: time.Now().Add(time.Hour)}
	uow := &fakeUoW{}
	uc := usecase.NewCancelBooking(&fakeBookingRepo{booking: b}, &fakePayments{outcome: outbound.OutcomeNoAttempt}, uow, fixedClock{t: time.Now()})
	_, err := uc.Cancel(context.Background(), "bk-1", visitor)
	require.NoError(t, err)
	assert.Equal(t, "CANCELLED", uow.expireSpec.Reason)
}
