package usecase

import (
	"context"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// LeaveQueue implements inbound.ILeaveUsecase: removes the caller's entry (design §2.5).
type LeaveQueue struct {
	store outbound.IQueueStore
	clock clock.IClock
}

// NewLeaveQueue accepts interfaces and returns the concrete usecase.
func NewLeaveQueue(store outbound.IQueueStore, clk clock.IClock) *LeaveQueue {
	return &LeaveQueue{store: store, clock: clk}
}

// Leave runs leave.lua atomically. A freed active slot is picked up by the Promotor next tick.
func (u *LeaveQueue) Leave(ctx context.Context, in inbound.LeaveInput) error {
	_, err := u.store.Leave(ctx, in.EventID, in.EntryID, in.VisitorID, u.clock.Now().UnixMilli())
	return err
}
