package worker

import (
	"context"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// Sweeper marks expired ADMITTED entries as MISSED and frees their slot (design §7.3).
// It does NOT publish booking.* events — Booking's own session sweeper releases the allocation
// and publishes booking.journey.ended when the claim window expires (spec 11.R2, K7).
type Sweeper struct {
	store    outbound.IQueueStore
	settings outbound.IEventSettingsRepo
	clock    clock.IClock
}

// NewSweeper accepts interfaces and returns the concrete worker.
func NewSweeper(store outbound.IQueueStore, settings outbound.IEventSettingsRepo, clk clock.IClock) *Sweeper {
	return &Sweeper{store: store, settings: settings, clock: clk}
}

// Run sweeps every configured event once.
func (s *Sweeper) Run(ctx context.Context) error {
	ids, err := s.settings.ListEventIDs(ctx)
	if err != nil {
		return err
	}
	for _, eventID := range ids {
		if err := s.runEvent(ctx, eventID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Sweeper) runEvent(ctx context.Context, eventID string) error {
	nowMs := s.clock.Now().UnixMilli()
	expired, err := s.store.ExpiredActive(ctx, eventID, nowMs)
	if err != nil {
		return err
	}
	for _, entryID := range expired {
		_, merr := s.store.MarkMissedAndClean(ctx, eventID, entryID, nowMs)
		if merr != nil {
			return merr
		}
		// Slot is now free; Promoter picks it up next tick.
		// Booking's session sweeper will release the allocation and publish
		// booking.journey.ended(UNCLAIMED) when its own claim window expires (K7).
	}
	return nil
}
