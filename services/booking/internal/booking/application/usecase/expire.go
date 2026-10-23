package usecase

import (
	"context"
	"time"

	vo "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/valueobject"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

// Expire expires or reviews a booking whose payment window lapsed. Design §7.2:
//   - Phase A (in-tx): PENDING_PAYMENT past deadline → VERIFYING + verification_until.
//   - Phase B (outside tx): call Payment resolve; SUCCEEDED → Settle directly (sweeper is the safety
//     net for lost webhooks); NOT_PAID/NO_ATTEMPT/grace-exhausted → uow.Expire.
type Expire struct {
	bookings           outbound.IBookingRepo
	payments           outbound.IPaymentClient
	uow                outbound.IUnitOfWork
	verifyGraceSeconds int
	ticketCodeFn       func(itemID string) string
	ticketLinkTokenFn  func(bookingID string) string // may be nil → empty token
}

// NewExpire wires the expiry usecase.
func NewExpire(
	bookings outbound.IBookingRepo,
	payments outbound.IPaymentClient,
	uow outbound.IUnitOfWork,
	verifyGraceSeconds int,
	ticketCodeFn func(itemID string) string,
	ticketLinkTokenFn func(bookingID string) string,
) *Expire {
	return &Expire{
		bookings:           bookings,
		payments:           payments,
		uow:                uow,
		verifyGraceSeconds: verifyGraceSeconds,
		ticketCodeFn:       ticketCodeFn,
		ticketLinkTokenFn:  ticketLinkTokenFn,
	}
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

	// Phase A: PENDING_PAYMENT past deadline → VERIFYING (in UoW tx).
	if booking.Status == vo.BookingPendingPayment {
		acted, err := uc.uow.StartVerifying(ctx, bookingID, uc.verifyGraceSeconds)
		if err != nil {
			return err
		}
		if !acted {
			return nil // not yet due or already terminal
		}
		// Re-read so Phase B sees the updated status and verification_until.
		booking, err = uc.bookings.GetByID(ctx, bookingID)
		if err != nil {
			return err
		}
	}

	// Phase B: VERIFYING → resolve (outside tx).
	if booking.Status != vo.BookingVerifying {
		return nil
	}

	now := time.Now().UTC()
	if booking.PaymentStartedAt != nil {
		res, rerr := uc.payments.Resolve(ctx, bookingID)
		if rerr != nil {
			// Payment service down (circuit open) → stay VERIFYING for now.
			return nil
		}
		if res.Paid {
			// Design §7.2 SUCCEEDED: sweeper settles directly.
			// Settle() is idempotent — if payment.succeeded consumer already ran, this is a no-op.
			_, err = uc.uow.Settle(ctx, outbound.SettleSpec{
				BookingID:         bookingID,
				SettlementTime:    res.SettlementTime,
				Method:            res.Method,
				TicketCodeFn:      uc.ticketCodeFn,
				TicketLinkTokenFn: uc.ticketLinkTokenFn,
				CorrelationID:     bookingID,
			})
			return err
		}
		// NOT_PAID or NO_ATTEMPT — check grace window before expiring.
		if booking.VerificationUntil != nil && now.Before(*booking.VerificationUntil) {
			return nil // still within grace, retry next tick
		}
	} else {
		// No payment attempt: check grace window, then treat as NO_ATTEMPT.
		if booking.VerificationUntil != nil && now.Before(*booking.VerificationUntil) {
			return nil
		}
	}

	reason := "PAYMENT_TIMEOUT"
	if booking.PaymentStartedAt == nil {
		reason = "SESSION_EXPIRED"
	}
	_, err = uc.uow.Expire(ctx, outbound.ExpireSpec{
		BookingID:     bookingID,
		Reason:        reason,
		Review:        false,
		CorrelationID: bookingID,
	})
	return err
}
