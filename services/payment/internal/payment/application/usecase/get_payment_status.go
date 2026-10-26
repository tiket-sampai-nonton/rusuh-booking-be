package usecase

import (
	"context"
	"fmt"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/mapper"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/port/outbound"
)

// GetPaymentStatus is the read-only status the payment page polls. It never changes state (invariant I-4).
type GetPaymentStatus struct {
	booking  outbound.IBookingClient
	attempts outbound.IAttemptRepository
	clock    clock.IClock
}

// NewGetPaymentStatus wires the usecase.
func NewGetPaymentStatus(b outbound.IBookingClient, a outbound.IAttemptRepository, clk clock.IClock) *GetPaymentStatus {
	return &GetPaymentStatus{booking: b, attempts: a, clock: clk}
}

// GetStatus returns the booking status and the latest attempt.
func (u *GetPaymentStatus) GetStatus(ctx context.Context, payer inbound.Payer, bookingID string) (dto.PaymentStatus, error) {
	p, err := loadOwnedPayable(ctx, u.booking, payer, bookingID)
	if err != nil {
		return dto.PaymentStatus{}, err
	}
	latest, found, err := u.attempts.FindLatest(ctx, p.BookingID)
	if err != nil {
		return dto.PaymentStatus{}, fmt.Errorf("%w: %w", inbound.ErrDependency, err)
	}
	out := dto.PaymentStatus{BookingID: p.BookingID, BookingStatus: p.Status, ServerTime: u.clock.Now().UTC()}
	if found {
		out.Attempt = mapper.AttemptPtrToDTO(&latest)
	}
	return out, nil
}
