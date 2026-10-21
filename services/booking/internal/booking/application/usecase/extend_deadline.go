package usecase

import (
	"context"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

// ExtendDeadline extends a booking's payment TTL once (gateway degradation).
type ExtendDeadline struct{ uow outbound.IUnitOfWork }

// NewExtendDeadline wires the usecase.
func NewExtendDeadline(uow outbound.IUnitOfWork) *ExtendDeadline { return &ExtendDeadline{uow: uow} }

// Extend validates the seconds value and calls the unit of work.
func (uc *ExtendDeadline) Extend(ctx context.Context, bookingID, reason string, seconds int) error {
	if seconds < 1 || seconds > 600 {
		return inbound.Validation("seconds", "seconds harus antara 1 dan 600")
	}
	return uc.uow.ExtendDeadline(ctx, outbound.DeadlineSpec{
		BookingID: bookingID,
		Reason:    reason,
		Seconds:   seconds,
	})
}
