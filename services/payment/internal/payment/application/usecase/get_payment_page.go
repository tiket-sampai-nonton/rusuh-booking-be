package usecase

import (
	"context"
	"fmt"
	"slices"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/mapper"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/port/outbound"
)

// GetPaymentPage builds the view model of the payment page (web and WebView).
type GetPaymentPage struct {
	booking  outbound.IBookingClient
	attempts outbound.IAttemptRepository
	methods  []string
	clock    clock.IClock
}

// NewGetPaymentPage wires the usecase; methods are the enabled payment methods (PAYMENT_METHODS_ENABLED).
func NewGetPaymentPage(b outbound.IBookingClient, a outbound.IAttemptRepository, methods []string, clk clock.IClock) *GetPaymentPage {
	return &GetPaymentPage{booking: b, attempts: a, methods: methods, clock: clk}
}

// GetPage returns amount, deadline, methods, and the open attempt (if any). 409 when Booking no longer waits for payment.
func (u *GetPaymentPage) GetPage(ctx context.Context, payer inbound.Payer, bookingID string) (dto.PaymentPage, error) {
	p, err := loadOwnedPayable(ctx, u.booking, payer, bookingID)
	if err != nil {
		return dto.PaymentPage{}, err
	}
	now := u.clock.Now()
	if !isPayable(p, now) {
		return dto.PaymentPage{}, inbound.ErrNotPayable
	}
	page := dto.PaymentPage{
		BookingID: p.BookingID, Methods: slices.Clone(u.methods), AmountMinor: p.AmountMinor,
		PaymentExpiresAt: p.PaymentExpiresAt.UTC(), ServerTime: now.UTC(), CanEdit: p.CanEdit,
	}
	open, found, err := u.attempts.FindOpen(ctx, p.BookingID)
	if err != nil {
		return dto.PaymentPage{}, fmt.Errorf("%w: %w", inbound.ErrDependency, err)
	}
	if found {
		page.CurrentAttempt = mapper.AttemptPtrToDTO(&open)
	}
	return page, nil
}
