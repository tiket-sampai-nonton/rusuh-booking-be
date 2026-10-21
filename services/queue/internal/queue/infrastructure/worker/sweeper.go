package worker

import (
	"context"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// outcomeUnclaimed is published to Booking when a turn deadline passes (design §7.3).
const outcomeUnclaimed = "UNCLAIMED"

// Sweeper marks expired ADMITTED entries as MISSED, frees their slot, and tells Booking to release
// the reservation (design §7.3).
type Sweeper struct {
	store    outbound.IQueueStore
	settings outbound.IEventSettingsRepo
	pub      outbound.IEventPublisher
	clock    clock.IClock
}

// NewSweeper accepts interfaces and returns the concrete worker.
func NewSweeper(store outbound.IQueueStore, settings outbound.IEventSettingsRepo, pub outbound.IEventPublisher, clk clock.IClock) *Sweeper {
	return &Sweeper{store: store, settings: settings, pub: pub, clock: clk}
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
		missed, merr := s.store.MarkMissedAndClean(ctx, eventID, entryID, nowMs)
		if merr != nil {
			return merr
		}
		if missed {
			_ = s.pub.PublishJourneyEnded(ctx, eventID, entryID, outcomeUnclaimed)
		}
	}
	return nil
}
