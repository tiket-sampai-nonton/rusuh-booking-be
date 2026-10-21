package usecase

import (
	"context"

	vo "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/valueobject"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

// Expire expires or reviews a booking whose payment window lapsed. When payment had started it asks the
// Payment Service to resolve: no payment → EXPIRED (release stock); payment found → REVIEW (hold stock,
// never oversell) — AC10.4/AC10.5.
type Expire struct {
	bookings outbound.IBookingRepo
	payments outbound.IPaymentClient
	uow      outbound.IUnitOfWork
}

// NewExpire wires the expiry usecase.
func NewExpire(bookings outbound.IBookingRepo, payments outbound.IPaymentClient, uow outbound.IUnitOfWork) *Expire {
	return &Expire{bookings: bookings, payments: payments, uow: uow}
}

// ExpireBooking resolves and finalises one booking.
func (uc *Expire) ExpireBooking(ctx context.Context, bookingID string) error {
	if !isUUID(bookingID) {
		return inbound.Validation("bookingId", "bookingId harus UUID")
	}
	booking, err := uc.bookings.GetByID(ctx, bookingID)
	if err != nil {
		return err
	}
	if !booking.Status.IsPayable() {
		return nil // already terminal; nothing to do (idempotent)
	}

	review := false
	reason := "PAYMENT_TIMEOUT"
	if booking.PaymentStartedAt != nil {
		res, rerr := uc.payments.Resolve(ctx, bookingID)
		if rerr != nil {
			return rerr
		}
		if res.Paid {
			review = true
		}
	}
	if booking.Status == vo.BookingPendingPayment && booking.PaymentStartedAt == nil {
		reason = "SESSION_EXPIRED"
	}

	_, err = uc.uow.Expire(ctx, outbound.ExpireSpec{
		BookingID:     bookingID,
		Reason:        reason,
		Review:        review,
		CorrelationID: bookingID,
	})
	return err
}
