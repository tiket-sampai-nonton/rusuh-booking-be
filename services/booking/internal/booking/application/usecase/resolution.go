package usecase

import (
	"context"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

// settleFound handles a Payment answer of SUCCEEDED for a booking that is still payable. A payment settled in time
// completes the booking; one settled after payment_expires_at cannot (the stock is released), so it becomes REVIEW for
// Ops (SRS-F-16-03). The time is the gateway's settlement time, not the moment Booking found out.
func settleFound(ctx context.Context, uow outbound.IUnitOfWork, b entity.Booking, res outbound.ResolveResult) error {
	if res.SettlementTime != nil && res.SettlementTime.After(b.PaymentExpiresAt) {
		_, err := uow.Expire(ctx, outbound.ExpireSpec{BookingID: b.ID, Reason: "PAYMENT_TIMEOUT", Review: true, CorrelationID: b.ID})
		return err
	}
	_, err := uow.Settle(ctx, outbound.SettleSpec{
		BookingID: b.ID, Method: "UNKNOWN", TicketCodeFn: ticketCodeFor(b.ID), CorrelationID: b.ID,
	})
	return err
}
