package usecase

import (
	"context"

	vo "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/valueobject"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

// Expire finishes a booking whose payment window lapsed. When payment had started it asks the Payment Service to
// resolve with CHECK_AND_CANCEL (which also closes the order at the gateway):
//   - SUCCEEDED in time → PAID; SUCCEEDED late → REVIEW;
//   - NOT_PAID / NO_ATTEMPT → EXPIRED, stock released;
//   - UNKNOWN or Payment unreachable → nothing changes and the stock stays held; the sweeper tries again.
//
// shortcut: no VERIFYING phase with a grace period (spec AC7-AC10): an unknown answer is retried on every sweep until
// Payment answers; add verification_until handling when a bounded wait is required.
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

	reason := "PAYMENT_TIMEOUT"
	if booking.PaymentStartedAt != nil {
		res, rerr := uc.payments.Resolve(ctx, bookingID, outbound.ResolveCheckAndCancel)
		if rerr != nil {
			return rerr
		}
		switch res.Outcome {
		case outbound.OutcomeSucceeded:
			return settleFound(ctx, uc.uow, booking, res)
		case outbound.OutcomeNotPaid, outbound.OutcomeNoAttempt:
		default: // UNKNOWN: Payment cannot say yet, so the stock must not be released
			return inbound.ErrDependency
		}
	}
	if booking.Status == vo.BookingPendingPayment && booking.PaymentStartedAt == nil {
		reason = "SESSION_EXPIRED"
	}

	_, err = uc.uow.Expire(ctx, outbound.ExpireSpec{
		BookingID:     bookingID,
		Reason:        reason,
		CorrelationID: bookingID,
	})
	return err
}
