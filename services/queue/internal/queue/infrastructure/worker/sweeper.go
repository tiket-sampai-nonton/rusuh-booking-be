package worker

import (
	"context"
	"errors"
	"fmt"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// SoldOutMaxBatches bounds the sold-out drain per event and tick (D-C8.4): 200 batches of outbound.SoldOutBatchSize.
const SoldOutMaxBatches = 200

// Sweeper marks expired ADMITTED entries as MISSED and frees their slot (design §7.3).
// It also ends CLAIMED slots past their claim bound (claimExpiresMs, QUEUE_CLAIMED_SLOT_MAX_SECONDS) as
// ENDED/CLAIM_TIMEOUT through end_entry.lua; Booking is not called for those.
// It also drains a sold-out event's WAITING/STALE entries with SOLD_OUT (D-C8.4): one batch after another while the
// batch comes back full, within SoldOutMaxBatches per event and tick. The next tick continues where this one stopped.
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
	if err := s.drainSoldOut(ctx, eventID, nowMs); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// drainSoldOut ends the WAITING/STALE entries of a sold-out event (D-C8.4). It stores no cursor: each batch removes what
// it scanned from waiting/stale, so the head of the set is the next batch. A short batch means the set is empty.
// The sale is re-read before every batch (R28): a sale that reopens (D-C5.3) stops the drain at once, so no further
// WAITING entry is ended with SOLD_OUT. The window that remains is the one batch already in flight.
func (s *Sweeper) drainSoldOut(ctx context.Context, eventID string, nowMs int64) error {
	for i := 0; i < SoldOutMaxBatches; i++ {
		sale, err := s.store.GetSale(ctx, eventID)
		if err != nil {
			return fmt.Errorf("sweeper get sale: %w", err)
		}
		if !sale.SoldOut {
			return nil
		}
		res, err := s.store.EndWaitingSoldOut(ctx, eventID, nowMs, outbound.SoldOutBatchSize)
		if err != nil {
			return fmt.Errorf("sweeper end waiting sold out: %w", err)
		}
		if res.Scanned < outbound.SoldOutBatchSize {
			return nil
		}
	}
	return nil // budget spent; the remainder waits for the next tick
}
