package usecase

import (
	"context"
	"strings"

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

	// Ask Payment first (CHECK_AND_CANCEL): a payment that raced in wins over the cancel, and an unclear answer means the
	// cancel is refused with 503 and nothing changes (stock is never released before the payment status is certain).
	res, err := uc.payments.Resolve(ctx, bookingID, outbound.ResolveCheckAndCancel)
	if err != nil {
		return dto.Booking{}, err
	}
	switch res.Outcome {
	case outbound.OutcomeSucceeded:
		err = settleFound(ctx, uc.uow, b, res) // never discard a paid booking
	case outbound.OutcomeNotPaid, outbound.OutcomeNoAttempt:
		_, err = uc.uow.Expire(ctx, outbound.ExpireSpec{BookingID: bookingID, Reason: "CANCELLED", CorrelationID: bookingID})
	default:
		return dto.Booking{}, inbound.ErrDependency
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

// ticketCodeFor derives a stable per-item ticket code: "TK-" plus 12 hex characters of the item id (48 bits). The code is
// deterministic, so a collision would make every retry of the settlement fail again (public_code is UNIQUE); with 8 characters
// (32 bits) that already had about a 1 in 4 chance at 50,000 tickets, with 12 it is about 4 in a million.
func ticketCodeFor(bookingID string) func(itemID string) string {
	return func(itemID string) string {
		hex := strings.ToUpper(strings.ReplaceAll(itemID, "-", ""))
		if hex == "" {
			hex = strings.ToUpper(strings.ReplaceAll(bookingID, "-", ""))
		}
		if len(hex) > 12 {
			hex = hex[:12]
		}
		return "TK-" + hex
	}
}
