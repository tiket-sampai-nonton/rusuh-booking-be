package usecase

import (
	"context"
	"errors"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

const sweepBatch = 100

// maxSweepRounds caps how many full batches one sweeper call drains (D-C7.3): a backlog larger than
// maxSweepRounds*sweepBatch is finished by the next tick instead of holding the worker for one long call.
const maxSweepRounds = 20

// sweepBudget bounds one sweep call (R32): a call starts no new round or row once lease − 3 s has passed, so every row it
// claimed is still leased while it works. Rows it did not reach lapse and are claimed again by a later call.
const sweepBudget = outbound.SweepLease - 3*time.Second

// SweepSessions releases admissions whose selection/claim window lapsed (AC10.1). Each admission is
// handled in its own transaction so one failure does not block the rest.
type SweepSessions struct {
	admissions outbound.IAdmissionRepo
	clock      clock.IClock
}

// NewSweepSessions wires the session sweeper.
func NewSweepSessions(admissions outbound.IAdmissionRepo, clk clock.IClock) *SweepSessions {
	return &SweepSessions{admissions: admissions, clock: clk}
}

// SweepSessions claims due admissions and releases each one. While a batch comes back full, it claims again (at most
// maxSweepRounds times); a short batch ends the call. The call stops once sweepBudget is spent.
func (uc *SweepSessions) SweepSessions(ctx context.Context) error {
	var errs []error
	start := time.Now()
	for round := 0; round < maxSweepRounds; round++ {
		if time.Since(start) >= sweepBudget {
			break
		}
		now := uc.clock.Now()
		batch, err := uc.admissions.ClaimDueSessions(ctx, now, sweepBatch)
		if err != nil {
			errs = append(errs, err)
			break
		}
		for _, a := range batch {
			if time.Since(start) >= sweepBudget {
				break
			}
			if err := uc.admissions.ExpireSession(ctx, a.ID, now); err != nil {
				errs = append(errs, err)
			}
		}
		if len(batch) < sweepBatch {
			break
		}
	}
	return errors.Join(errs...)
}

// SweepExpiries scans bookings past their payment window and finalises each via the expiry usecase.
type SweepExpiries struct {
	bookings outbound.IBookingDueScanner
	expiry   inbound.IExpiryUsecase
	clock    clock.IClock
}

// NewSweepExpiries wires the expiry sweeper.
func NewSweepExpiries(bookings outbound.IBookingDueScanner, expiry inbound.IExpiryUsecase, clk clock.IClock) *SweepExpiries {
	return &SweepExpiries{bookings: bookings, expiry: expiry, clock: clk}
}

// SweepExpiries claims due bookings and resolves each one, repeating while a batch comes back full and stopping once
// sweepBudget is spent (see SweepSessions).
func (uc *SweepExpiries) SweepExpiries(ctx context.Context) error {
	var errs []error
	start := time.Now()
	for round := 0; round < maxSweepRounds; round++ {
		if time.Since(start) >= sweepBudget {
			break
		}
		ids, err := uc.bookings.ClaimDueBookings(ctx, uc.clock.Now(), sweepBatch)
		if err != nil {
			errs = append(errs, err)
			break
		}
		for _, id := range ids {
			if time.Since(start) >= sweepBudget {
				break
			}
			if err := uc.expiry.ExpireBooking(ctx, id); err != nil {
				errs = append(errs, err)
			}
		}
		if len(ids) < sweepBatch {
			break
		}
	}
	return errors.Join(errs...)
}

// availabilityTTL outlives several worker ticks, so a briefly stalled projector does not blank the stream.
const availabilityTTL = 30 * time.Second

// ProjectAvailability writes the live per-category availability of every on-sale event to the cache the
// SSE stream reads. One event failing does not stop the others.
type ProjectAvailability struct {
	source outbound.IAvailabilitySource
	cache  outbound.IAvailabilityCache
	clock  clock.IClock
}

// NewProjectAvailability wires the availability projector.
func NewProjectAvailability(source outbound.IAvailabilitySource, cache outbound.IAvailabilityCache, clk clock.IClock) *ProjectAvailability {
	return &ProjectAvailability{source: source, cache: cache, clock: clk}
}

// ProjectAvailability refreshes the snapshot of each on-sale event.
func (uc *ProjectAvailability) ProjectAvailability(ctx context.Context) error {
	byEvent, err := uc.source.OnSaleAvailability(ctx, uc.clock.Now())
	if err != nil {
		return err
	}
	var errs []error
	for eventID, snap := range byEvent {
		if err := uc.cache.SetAvailability(ctx, eventID, snap, availabilityTTL); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
