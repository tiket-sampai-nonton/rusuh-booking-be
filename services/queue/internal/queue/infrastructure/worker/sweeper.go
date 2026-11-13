package worker

import (
	"context"
	"errors"
	"fmt"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// Sweeper marks expired ADMITTED entries as MISSED and frees their slot (design §7.3).
// It also ends CLAIMED slots past their claim bound (claimExpiresMs, QUEUE_CLAIMED_SLOT_MAX_SECONDS) as
// ENDED/CLAIM_TIMEOUT through end_entry.lua; Booking is not called for those.
// It does NOT publish booking.* events — Booking's own session sweeper releases the allocation
// and publishes booking.journey.ended when the claim window expires (spec 11.R2, K7).
type Sweeper struct {
	store    outbound.IQueueStore
	settings outbound.IEventSettingsRepo
	clock    clock.IClock
	batchMax int
}

// NewSweeper accepts interfaces and returns the concrete worker.
func NewSweeper(store outbound.IQueueStore, settings outbound.IEventSettingsRepo, clk clock.IClock, batchMax int) *Sweeper {
	if batchMax <= 0 {
		batchMax = 500
	}
	return &Sweeper{store: store, settings: settings, clock: clk, batchMax: batchMax}
}

// Run sweeps every configured event once.
func (s *Sweeper) Run(ctx context.Context) error {
	ids, err := s.settings.ListEventIDs(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, eventID := range ids {
		if err := s.runEvent(ctx, eventID); err != nil {
			errs = append(errs, fmt.Errorf("sweeper event %s: %w", eventID, err))
		}
	}
	return errors.Join(errs...)
}

func (s *Sweeper) runEvent(ctx context.Context, eventID string) error {
	nowMs := s.clock.Now().UnixMilli()
	expired, err := s.store.ExpiredActive(ctx, eventID, nowMs, s.batchMax)
	if err != nil {
		return err
	}
	var errs []error
	for _, entryID := range expired {
		// One entry's failure must not starve the rest of the page; a failed entry is still in active and is
		// returned by ExpiredActive again on the next tick.
		if _, merr := s.store.EndEntry(ctx, eventID, entryID, outbound.EndSweepMissed, nowMs); merr != nil {
			errs = append(errs, fmt.Errorf("end entry %s: %w", entryID, merr))
			continue
		}
		// Slot is now free; Promoter picks it up next tick.
		// Booking's session sweeper will release the allocation and publish
		// booking.journey.ended(UNCLAIMED) when its own claim window expires (K7).
	}
	return errors.Join(errs...)
}
