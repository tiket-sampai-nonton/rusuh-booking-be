package usecase

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

// PublishedOutboxRetention keeps published outbox rows for 7 days (D-C7.8).
const PublishedOutboxRetention = 7 * 24 * time.Hour

// AttendeeCheckRetention keeps an admission's attendee checks until 1 day after the admission ended (D-C7.8).
const AttendeeCheckRetention = 24 * time.Hour

// purgeBatch is the row limit of one DELETE; maxPurgeRounds caps the full batches one table gets per run (D-C7.8).
const (
	purgeBatch     = 1000
	maxPurgeRounds = 20
)

// Purge deletes what the retention windows no longer need: published outbox rows, expired idempotency records and
// attendee checks of ended admissions (AC-C7.8).
type Purge struct {
	retention outbound.IRetention
	clock     clock.IClock
}

// NewPurge wires the retention job.
func NewPurge(retention outbound.IRetention, clk clock.IClock) *Purge {
	return &Purge{retention: retention, clock: clk}
}

// Run drains each table in batches of purgeBatch, repeating while a batch comes back full, for at most maxPurgeRounds
// rounds per table; a larger backlog is finished by the next tick. A failing table does not stop the other two.
func (uc *Purge) Run(ctx context.Context) error {
	now := uc.clock.Now()
	return errors.Join(
		drainPurge("outbox", func(limit int) (int, error) {
			return uc.retention.PurgePublishedOutbox(ctx, now.Add(-PublishedOutboxRetention), limit)
		}),
		drainPurge("idempotency", func(limit int) (int, error) {
			return uc.retention.PurgeExpiredIdempotency(ctx, now, limit)
		}),
		drainPurge("attendee-checks", func(limit int) (int, error) {
			return uc.retention.PurgeAttendeeChecks(ctx, now.Add(-AttendeeCheckRetention), limit)
		}),
	)
}

// drainPurge runs one table's delete until a batch comes back short or maxPurgeRounds full batches have been deleted.
func drainPurge(table string, purge func(limit int) (int, error)) error {
	for round := 0; round < maxPurgeRounds; round++ {
		n, err := purge(purgeBatch)
		if err != nil {
			return fmt.Errorf("purge %s: %w", table, err)
		}
		if n < purgeBatch {
			return nil
		}
	}
	return nil
}
