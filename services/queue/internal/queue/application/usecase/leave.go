package usecase

import (
	"context"
	"log/slog"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// LeaveQueue implements inbound.ILeaveUsecase: removes the caller's entry (design §2.5, §5.2).
type LeaveQueue struct {
	store   outbound.IQueueStore
	booking outbound.IBookingClient
	clock   clock.IClock
	logger  *slog.Logger
}

// NewLeaveQueue accepts interfaces and returns the concrete usecase.
func NewLeaveQueue(store outbound.IQueueStore, booking outbound.IBookingClient, clk clock.IClock) *LeaveQueue {
	return &LeaveQueue{store: store, booking: booking, clock: clk, logger: slog.Default()}
}

// Leave removes the entry atomically in Valkey FIRST: leave.lua decides ownership and status in one step and answers
// NOT_FOUND for a stranger without changing anything. Only when it reports that it moved an ADMITTED entry to LEFT
// does the usecase release the Booking allocation (saga compensation, spec 14.R1, req 7.2, ADR-018). Doing the
// release before the commit (as before) let anyone who knew an entry id free another buyer's reserved ticket, since
// the stranger was only rejected afterwards. Release failure is best-effort: Booking's own session sweeper
// releases the allocation when the claim window expires (K7).
func (u *LeaveQueue) Leave(ctx context.Context, in inbound.LeaveInput) error {
	if err := checkTokenGen(ctx, u.store, in.EventID, in.VisitorID, in.Gen); err != nil {
		return err
	}
	res, err := u.store.Leave(ctx, in.EventID, in.EntryID, in.VisitorID, u.clock.Now().UnixMilli())
	if err != nil {
		return err
	}
	if res.Tag == "OK" && res.PrevStatus == "ADMITTED" && res.AllocationID != "" {
		if rerr := u.booking.ReleaseAllocation(ctx, res.AllocationID, outbound.ReleaseReasonEntryGone); rerr != nil {
			u.logger.Warn("leave: gagal melepas alokasi Booking; sweeper akan membersihkan saat deadline",
				"eventId", in.EventID, "entryId", in.EntryID, "allocationId", res.AllocationID, "error", rerr)
		}
	}
	return nil
}
