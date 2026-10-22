package usecase

import (
	"context"
	"log/slog"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/valueobject"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// LeaveQueue implements inbound.ILeaveUsecase: removes the caller's entry (design §2.5).
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

// Leave removes the entry atomically. If the entry was ADMITTED, it releases the allocation as
// saga compensation before leaving (spec 14.R1, req 7.2, ADR-018). Release failure is best-effort:
// Booking's own session sweeper releases the allocation when the claim window expires (K7).
func (u *LeaveQueue) Leave(ctx context.Context, in inbound.LeaveInput) error {
	ent, found, err := u.store.GetEntry(ctx, in.EventID, in.EntryID)
	if err != nil {
		return err
	}
	if found && ent.Status == valueobject.StatusAdmitted && ent.AllocationID != "" {
		if rerr := u.booking.ReleaseAllocation(ctx, ent.AllocationID, "QUEUE_ENTRY_GONE"); rerr != nil {
			// Best-effort: log and continue. Booking sweeper handles the fallback on claim deadline.
			u.logger.Warn("leave: gagal melepas alokasi Booking; sweeper akan membersihkan saat deadline",
				"allocationId", ent.AllocationID, "error", rerr)
		}
	}
	_, err = u.store.Leave(ctx, in.EventID, in.EntryID, in.VisitorID, u.clock.Now().UnixMilli())
	return err
}
