package usecase

import (
	"context"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

// GetPayable returns the billing view of a booking for Payment Service.
type GetPayable struct {
	repo  outbound.IBookingRepo
	clock clock.IClock
}

// NewGetPayable wires the usecase.
func NewGetPayable(repo outbound.IBookingRepo, clk clock.IClock) *GetPayable {
	return &GetPayable{repo: repo, clock: clk}
}

// GetPayable returns a Payable DTO or an error if the booking is not in a payable state.
func (uc *GetPayable) GetPayable(ctx context.Context, bookingID string) (dto.Payable, error) {
	b, err := uc.repo.GetByID(ctx, bookingID)
	if err != nil {
		return dto.Payable{}, err
	}
	// Only PENDING_PAYMENT and VERIFYING are payable (domain §6.2).
	if !b.Status.IsPayable() {
		return dto.Payable{}, inbound.ErrNotPayable
	}
	if !b.PaymentExpiresAt.IsZero() && b.IsExpired(uc.clock.Now()) {
		return dto.Payable{}, inbound.ErrPaymentExpired
	}
	return dto.Payable{
		BookingID:        b.ID,
		PublicRef:        b.PublicRef,
		Status:           string(b.Status),
		AmountMinor:      b.AmountMinor,
		PaymentExpiresAt: b.PaymentExpiresAt,
		VisitorID:        b.VisitorID,
		Version:          int(b.Version),
	}, nil
}
