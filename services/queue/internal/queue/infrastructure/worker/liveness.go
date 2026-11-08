package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// LivenessTuning holds the heartbeat thresholds (R03.8) and the per-tick batch.
type LivenessTuning struct {
	StaleAfter time.Duration
	EndAfter   time.Duration
	BatchMax   int
	LeaseTTL   time.Duration
}

// Liveness marks silent WAITING entries STALE and ends long-silent ones (ADR-030, R03). It runs under the
// per-event promoter lease (non-blocking: a busy event is skipped this tick), which makes "never admitted while
// STALE" structural: an entry cannot turn STALE between the promoter's candidate read and promote.lua. The
// sweep fans out over the sharded liveness index hb:{s}/hbstale:{s} (ADR-030 Amendment).
type Liveness struct {
	store    outbound.IQueueStore
	settings outbound.IEventSettingsRepo
	clock    clock.IClock
	tuning   LivenessTuning
	logger   *slog.Logger

	// leaseHeld counts consecutive passes per event that found the promoter lease held. It is touched only from
	// Run, which the runner calls from a single goroutine (one goroutine per job), so it needs no mutex.
	leaseHeld map[string]int
}

// leaseHeldWarnEvery is the number of consecutive skipped passes between starvation warnings (~30 s at 5 s).
const leaseHeldWarnEvery = 6

// NewLiveness accepts interfaces and returns the concrete worker.
func NewLiveness(store outbound.IQueueStore, settings outbound.IEventSettingsRepo, clk clock.IClock, t LivenessTuning) *Liveness {
	if t.BatchMax <= 0 {
		t.BatchMax = 1000
	}
	if t.LeaseTTL <= 0 {
		t.LeaseTTL = 10 * time.Second
	}
	return &Liveness{store: store, settings: settings, clock: clk, tuning: t, logger: slog.Default(), leaseHeld: map[string]int{}}
}

// WithLogger replaces the default logger.
func (l *Liveness) WithLogger(lg *slog.Logger) *Liveness {
	l.logger = lg
	return l
}

// Run sweeps every configured event once; one event's failure never stops the others.
func (l *Liveness) Run(ctx context.Context) error {
	ids, err := l.settings.ListEventIDs(ctx)
	if err != nil {
		return err
	}
	l.evictRemoved(ids)
	var errs []error
	for _, eventID := range ids {
		if err := l.runEvent(ctx, eventID); err != nil {
			errs = append(errs, fmt.Errorf("liveness event %s: %w", eventID, err))
		}
	}
	return errors.Join(errs...)
}

// evictRemoved drops starvation counters of events no longer configured, so the map stays bounded.
func (l *Liveness) evictRemoved(ids []string) {
	if len(l.leaseHeld) == 0 {
		return
	}
	keep := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		keep[id] = struct{}{}
	}
	for id := range l.leaseHeld {
		if _, ok := keep[id]; !ok {
			delete(l.leaseHeld, id)
		}
	}
}

func (l *Liveness) runEvent(ctx context.Context, eventID string) error {
	// One round-trip: acquire-sweep-release inside liveness_tick.lua. Separate acquire/sweep/release calls each queue
	// behind heartbeat traffic on Valkey's single command thread and push the promoter tick past +20 % of base
	// (design-change §4.3). A busy event (lease held by the promoter) is skipped this tick via LEASE_LOST.
	now := l.clock.Now().UnixMilli()
	res, _, err := l.store.SweepLivenessTick(ctx, eventID, now-l.tuning.StaleAfter.Milliseconds(), now-l.tuning.EndAfter.Milliseconds(), l.tuning.BatchMax, now, l.tuning.LeaseTTL, 0)
	if err != nil {
		return err
	}
	if res.LeaseLost {
		// Another holder owns the lease this tick: nothing changed. Signal prolonged starvation (e.g. a Booking
		// outage keeping the promoter inside slow calls) without escalating it to an error.
		l.leaseHeld[eventID]++
		if n := l.leaseHeld[eventID]; n%leaseHeldWarnEvery == 0 {
			l.logger.Warn("liveness lease held, sweep skipped", "eventId", eventID, "consecutive", n)
		}
		return nil
	}
	delete(l.leaseHeld, eventID)
	if res.Staled > 0 || res.Ended > 0 {
		l.logger.Info("liveness", "eventId", eventID, "staled", res.Staled, "ended", res.Ended)
	}
	return nil
}
