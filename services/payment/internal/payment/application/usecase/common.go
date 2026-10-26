// Package usecase holds the application logic of the Payment domain: one usecase per file, depending only on ports.
package usecase

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/entity"
	vo "github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/valueobject"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/port/outbound"
)

// Routing keys published by Payment (no version suffix, same as the Booking consumer; payload follows *.v1 schemas).
const (
	routingPaymentSucceeded = "payment.succeeded"
	routingPaymentFailed    = "payment.failed"
)

// Booking statuses Payment accepts a payment for (internal-booking.yaml Payable.status).
const (
	bookingPendingPayment = "PENDING_PAYMENT"
	bookingVerifying      = "VERIFYING"
)

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func isUUID(s string) bool { return uuidRe.MatchString(s) }

// loadOwnedPayable returns the booking's payable data when the caller owns it. A booking that is not the
// caller's, or an id that is not a UUID, is "not found" so the response never reveals who owns a booking (OQ-19).
func loadOwnedPayable(ctx context.Context, b outbound.IBookingClient, payer inbound.Payer, bookingID string) (outbound.Payable, error) {
	if !isUUID(bookingID) || (payer.BookingID != "" && payer.BookingID != bookingID) {
		return outbound.Payable{}, inbound.ErrNotFound
	}
	p, err := b.GetPayable(ctx, bookingID)
	switch {
	case errors.Is(err, outbound.ErrBookingNotFound):
		return outbound.Payable{}, inbound.ErrNotFound
	case err != nil:
		return outbound.Payable{}, fmt.Errorf("%w: %w", inbound.ErrDependency, err)
	}
	if p.VisitorID == "" || p.VisitorID != payer.VisitorID {
		return outbound.Payable{}, inbound.ErrNotFound
	}
	return p, nil
}

// isPayable reports whether Booking still waits for payment and the deadline has not passed.
func isPayable(p outbound.Payable, now time.Time) bool {
	return (p.Status == bookingPendingPayment || p.Status == bookingVerifying) && now.Before(p.PaymentExpiresAt)
}

// applySuccess moves an attempt to SUCCEEDED and writes payment.succeeded in the same transaction. settledAt is
// the provider's settlement time; when the provider did not give one, now is used. Returns false when the attempt
// already succeeded (duplicate) or cannot move (FAILED).
func applySuccess(ctx context.Context, repo outbound.IAttemptRepository, a entity.Attempt, txID, rawStatus string, settledAt *time.Time, now time.Time) (bool, error) {
	t := now
	if settledAt != nil {
		t = *settledAt
	}
	t = t.UTC()
	return repo.Transition(ctx, outbound.TransitionSpec{
		AttemptID: a.ID, To: vo.AttemptSucceeded, ProviderTxID: txID, SettlementTime: &t, LastProviderStatus: rawStatus,
		Event: &outbound.Event{RoutingKey: routingPaymentSucceeded, Payload: dto.PaymentSucceeded{
			BookingID: a.BookingID, AttemptID: a.ID, SettlementTime: t, AmountMinor: a.AmountMinor, Method: string(a.Method),
		}},
	})
}

// applyFailure moves an attempt to FAILED and writes payment.failed.
func applyFailure(ctx context.Context, repo outbound.IAttemptRepository, a entity.Attempt, rawStatus string) (bool, error) {
	return repo.Transition(ctx, outbound.TransitionSpec{
		AttemptID: a.ID, To: vo.AttemptFailed, LastProviderStatus: rawStatus,
		Event: &outbound.Event{RoutingKey: routingPaymentFailed, Payload: dto.PaymentFailed{
			BookingID: a.BookingID, AttemptID: a.ID, Reason: failureReason(rawStatus),
		}},
	})
}

func failureReason(raw string) string {
	if raw == "" {
		return "PAYMENT_FAILED"
	}
	return strings.ToUpper(raw)
}

// parseGrossAmount reads a Midtrans gross_amount ("1500000.00" or "1500000") as whole rupiah. Fractions other
// than zero are rejected: IDR has no minor units.
func parseGrossAmount(s string) (int64, error) {
	whole, frac, _ := strings.Cut(strings.TrimSpace(s), ".")
	if strings.Trim(frac, "0") != "" {
		return 0, errors.New("gross_amount berpecahan")
	}
	n, err := strconv.ParseInt(whole, 10, 64)
	if err != nil || n < 0 {
		return 0, errors.New("gross_amount tidak valid")
	}
	return n, nil
}
