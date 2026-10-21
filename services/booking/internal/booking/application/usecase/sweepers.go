package usecase

import (
	"context"
	"errors"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

const sweepBatch = 100

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

// SweepSessions scans due admissions and releases each one.
func (uc *SweepSessions) SweepSessions(ctx context.Context) error {
	now := uc.clock.Now()
	due, err := uc.admissions.DueSessions(ctx, now, sweepBatch)
	if err != nil {
		return err
	}
	var errs []error
	for _, a := range due {
		if err := uc.admissions.ExpireSession(ctx, a.ID, now); err != nil {
			errs = append(errs, err)
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

// SweepExpiries resolves each due booking.
func (uc *SweepExpiries) SweepExpiries(ctx context.Context) error {
	ids, err := uc.bookings.DueBookingIDs(ctx, uc.clock.Now(), sweepBatch)
	if err != nil {
		return err
	}
	var errs []error
	for _, id := range ids {
		if err := uc.expiry.ExpireBooking(ctx, id); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
