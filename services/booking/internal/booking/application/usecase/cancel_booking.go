package usecase

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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

// NewTicketCode returns a new ticket code: "TK-" plus 20 hex characters (80 random bits). The code is also the QR payload,
// so it must not be guessable (a 32-bit code is brute-forceable and collides at tens of thousands of tickets). A collision
// (public_code is UNIQUE) is astronomically unlikely and a retry draws a new code.
func NewTicketCode() string {
	var b [10]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("crypto/rand: " + err.Error()) // the platform CSPRNG failing is unrecoverable
	}
	return "TK-" + strings.ToUpper(hex.EncodeToString(b[:]))
}
