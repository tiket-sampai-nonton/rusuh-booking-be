package usecase

import (
	"context"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
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

// GetPayable returns the billing view of a booking in whatever status it is in (not found is the only error).
func (uc *GetPayable) GetPayable(ctx context.Context, bookingID string) (dto.Payable, error) {
	b, err := uc.repo.GetByID(ctx, bookingID)
	if err != nil {
		return dto.Payable{}, err
	}
	// Any status is returned: Payment decides whether the booking can still be charged (it reads Status and
	// PaymentExpiresAt) and needs the real status to answer GET /payments/{id}/status after PAID or EXPIRED.
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
