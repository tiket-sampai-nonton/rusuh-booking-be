package usecase

import (
	"context"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

// PaymentLock atomically locks a booking for a payment attempt.
type PaymentLock struct {
	uow   outbound.IUnitOfWork
	clock clock.IClock
}

// NewPaymentLock wires the usecase.
func NewPaymentLock(uow outbound.IUnitOfWork, clk clock.IClock) *PaymentLock {
	return &PaymentLock{uow: uow, clock: clk}
}

// Lock validates the attemptKey, then delegates to the unit of work.
func (uc *PaymentLock) Lock(ctx context.Context, bookingID, attemptKey string) (dto.PaymentLockResponse, error) {
	if len(attemptKey) < 8 {
		return dto.PaymentLockResponse{}, inbound.Validation("attemptKey", "attemptKey harus minimal 8 karakter")
	}
	res, err := uc.uow.PaymentLock(ctx, outbound.PaymentLockSpec{
		BookingID:  bookingID,
		AttemptKey: attemptKey,
	})
	if err != nil {
		return dto.PaymentLockResponse{}, err
	}
	return dto.PaymentLockResponse{
		Outcome:          res.Outcome,
		Version:          res.Version,
		AmountMinor:      res.AmountMinor,
		PaymentExpiresAt: res.PaymentExpiresAt,
	}, nil
}
