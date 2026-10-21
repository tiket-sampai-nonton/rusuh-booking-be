package usecase

import (
	"context"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/mapper"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

// CancelBooking cancels an unpaid booking at the buyer's request. If a payment raced in at the gateway
// it settles instead of cancelling, so a paid booking is never thrown away (invariant: status only ever
// moves forward, and a real payment always wins over a cancel). Idempotent for terminal bookings.
type CancelBooking struct {
	bookings outbound.IBookingRepo
	payments outbound.IPaymentClient
	uow      outbound.IUnitOfWork
	clock    clock.IClock
}

// NewCancelBooking wires the usecase.
func NewCancelBooking(
	bookings outbound.IBookingRepo,
	payments outbound.IPaymentClient,
	uow outbound.IUnitOfWork,
	clk clock.IClock,
) *CancelBooking {
	return &CancelBooking{bookings: bookings, payments: payments, uow: uow, clock: clk}
}

// Cancel cancels the booking, or settles it if a gateway payment is found.
func (uc *CancelBooking) Cancel(ctx context.Context, bookingID, visitorID string) (dto.Booking, error) {
	b, err := uc.bookings.GetByID(ctx, bookingID)
	if err != nil {
		return dto.Booking{}, err
	}
	if b.VisitorID != visitorID {
		return dto.Booking{}, inbound.ErrVisitorMismatch
	}
	// Already terminal: return current state (idempotent).
	if !b.Status.IsPayable() {
		return mapper.ToBooking(b, 0, uc.clock.Now()), nil
	}
	// Payment lock acquired — a gateway attempt already started, so cancel is forbidden.
	if b.IsLocked() {
		return dto.Booking{}, inbound.ErrBookingLocked
	}

	// Check the gateway for a race-condition payment that landed before the cancel.
	res, err := uc.payments.Resolve(ctx, bookingID)
	if err != nil {
		return dto.Booking{}, err
	}

	if res.Paid {
		// Race: payment found — settle instead of cancel (never discard a paid booking).
		_, err = uc.uow.Settle(ctx, outbound.SettleSpec{
			BookingID:     bookingID,
			Method:        "UNKNOWN",
			TicketCodeFn:  ticketCodeFor(bookingID),
			CorrelationID: bookingID,
		})
	} else {
		// No payment — expire with the CANCELLED reason, releasing held stock.
		_, err = uc.uow.Expire(ctx, outbound.ExpireSpec{
			BookingID:     bookingID,
			Reason:        "CANCELLED",
			Review:        false,
			CorrelationID: bookingID,
		})
	}
	if err != nil {
		return dto.Booking{}, err
	}

	// Re-read the updated state to return the current projection.
	b, err = uc.bookings.GetByID(ctx, bookingID)
	if err != nil {
		return dto.Booking{}, err
	}
	return mapper.ToBooking(b, 0, uc.clock.Now()), nil
}

// ticketCodeFor derives a stable per-item ticket code without panicking on short IDs.
func ticketCodeFor(bookingID string) func(itemID string) string {
	return func(itemID string) string {
		suffix := itemID
		if len(suffix) > 8 {
			suffix = suffix[:8]
		}
		if suffix == "" {
			suffix = bookingID
		}
		return "TK-" + suffix
	}
}
