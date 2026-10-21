package usecase

import (
	"context"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

// Settle settles a paid booking (consumed from payment.succeeded). Idempotency is enforced by the unit
// of work's state guard (status != PAID) plus ON CONFLICT on tickets — invariant #5.
type Settle struct {
	uow outbound.IUnitOfWork
	ids IDGen
}

// NewSettle wires the settlement usecase.
func NewSettle(uow outbound.IUnitOfWork, ids IDGen) *Settle {
	return &Settle{uow: uow, ids: ids}
}

// Settle applies the settlement. A duplicate delivery is a harmless no-op.
func (uc *Settle) Settle(ctx context.Context, cmd inbound.SettleCommand) error {
	if !isUUID(cmd.BookingID) {
		return inbound.Validation("bookingId", "bookingId harus UUID")
	}
	_, err := uc.uow.Settle(ctx, outbound.SettleSpec{
		BookingID:     cmd.BookingID,
		Method:        cmd.Method,
		TicketCodeFn:  func(string) string { return uc.ids.NewPublicRef() },
		CorrelationID: cmd.CorrelationID,
	})
	return err
}
