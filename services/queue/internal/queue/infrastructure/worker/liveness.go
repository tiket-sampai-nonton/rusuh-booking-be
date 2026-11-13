package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// LivenessTuning holds the heartbeat thresholds (R03.8) and the per-tick budget (BatchMax counts the entries staled
// plus ended per event and tick, across all shards).
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
//
// Liveness iterates every configured event, not only the active ones (R40): an event past its cut-off can still hold
// entries that heartbeat, and only this sweep ends them. Once an inactive event has been swept, liveness also retires it
// (RetireEvent), which the store refuses while anything is live; a retirement that succeeded is remembered so it is not
// repeated on every tick.
type Liveness struct {
	store    outbound.IQueueStore
	settings outbound.IEventSettingsRepo
	clock    clock.IClock
	tuning   LivenessTuning
	logger   *slog.Logger

	// leaseHeld counts consecutive passes per event that found the promoter lease held. It is touched only from
	// Run, which the runner calls from a single goroutine (one goroutine per job), so it needs no mutex.
	leaseHeld map[string]int

	retiredMu sync.Mutex          // guards retired
	retired   map[string]struct{} // inactive events this process has retired (R40); pruned like leaseHeld

	// tick counts the sweeps run by Run. Its value is the shard each sweep begins at, so the budget of a tick that
	// falls short of some shards is spent on other shards in the next tick (C8.7). Run is single-goroutine too.
	tick int
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
	return &Liveness{store: store, settings: settings, clock: clk, tuning: t, logger: slog.Default(),
		leaseHeld: map[string]int{}, retired: map[string]struct{}{}}
}

// WithLogger replaces the default logger.
func (l *Liveness) WithLogger(lg *slog.Logger) *Liveness {
	l.logger = lg
	return l
}

// Run sweeps every configured event once (R40); one event's failure never stops the others. An event outside the active
// list (D-C8.10) is retired after its sweep, once it has been swept without a lost lease.
func (l *Liveness) Run(ctx context.Context) error {
	all, err := l.settings.ListEventIDs(ctx)
	if err != nil {
		return err
	}
	active, err := l.settings.ListActiveEventIDs(ctx, l.clock.Now())
	if err != nil {
		return err
	}
	isActive := make(map[string]bool, len(active))
	for _, id := range active {
		isActive[id] = true
	}
	l.evictRemoved(all)
	// One start shard per tick, shared by every event of the tick.
	startShard := l.tick
	l.tick++
	var errs []error
	for _, eventID := range all {
		swept, err := l.runEvent(ctx, eventID, startShard)
		if err != nil {
			errs = append(errs, fmt.Errorf("liveness event %s: %w", eventID, err))
			continue
		}
		if isActive[eventID] {
			l.forgetRetired(eventID)
			continue
		}
		if swept {
			if err := l.retireInactive(ctx, eventID); err != nil {
				errs = append(errs, fmt.Errorf("liveness retire event %s: %w", eventID, err))
			}
		}
	}
	return errors.Join(errs...)
}

// evictRemoved drops starvation counters and retirement marks of events no longer configured, so the maps stay bounded.
func (l *Liveness) evictRemoved(ids []string) {
	keep := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		keep[id] = struct{}{}
	}
	for id := range l.leaseHeld {
		if _, ok := keep[id]; !ok {
			delete(l.leaseHeld, id)
		}
	}
	l.retiredMu.Lock()
	defer l.retiredMu.Unlock()
	for id := range l.retired {
		if _, ok := keep[id]; !ok {
			delete(l.retired, id)
		}
	}
}

// retireInactive asks the store to retire an inactive event (R40). The store refuses while a waiting, stale or active entry
// (or a heartbeat index entry) is left: that refusal is not remembered, so the next tick asks again. The stored sale window
// already proves the sale ended more than a day ago, so no Booking read is needed. Retired events are remembered.
func (l *Liveness) retireInactive(ctx context.Context, eventID string) error {
	l.retiredMu.Lock()
	_, done := l.retired[eventID]
	l.retiredMu.Unlock()
	if done {
		return nil
	}
	ok, err := l.store.RetireEvent(ctx, eventID, outbound.EntryRetention)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	l.retiredMu.Lock()
	l.retired[eventID] = struct{}{}
	l.retiredMu.Unlock()
	l.logger.Info("liveness: inactive event retired", "eventId", eventID)
	return nil
}

// forgetRetired clears the retirement mark of an event that is active again (an operator set its window back to NULL), so
// it is retired again when it turns inactive.
func (l *Liveness) forgetRetired(eventID string) {
	l.retiredMu.Lock()
	defer l.retiredMu.Unlock()
	delete(l.retired, eventID)
}

// runEvent sweeps one event. It reports whether the sweep ran (false when the promoter lease was held).
func (l *Liveness) runEvent(ctx context.Context, eventID string, startShard int) (bool, error) {
	// One round-trip: acquire-sweep-release inside liveness_tick.lua. Separate acquire/sweep/release calls each queue
	// behind heartbeat traffic on Valkey's single command thread and push the promoter tick past +20 % of base
	// (design-change §4.3). A busy event (lease held by the promoter) is skipped this tick via LEASE_LOST.
	now := l.clock.Now().UnixMilli()
	res, _, err := l.store.SweepLivenessTick(ctx, eventID, now-l.tuning.StaleAfter.Milliseconds(), now-l.tuning.EndAfter.Milliseconds(), l.tuning.BatchMax, now, l.tuning.LeaseTTL, 0, startShard)
	if err != nil {
		return false, err
	}
	if res.LeaseLost {
		// Another holder owns the lease this tick: nothing changed. Signal prolonged starvation (e.g. a Booking
		// outage keeping the promoter inside slow calls) without escalating it to an error.
		l.leaseHeld[eventID]++
		if n := l.leaseHeld[eventID]; n%leaseHeldWarnEvery == 0 {
			l.logger.Warn("liveness lease held, sweep skipped", "eventId", eventID, "consecutive", n)
		}
		return false, nil
	}
	delete(l.leaseHeld, eventID)
	if res.Staled > 0 || res.Ended > 0 {
		l.logger.Info("liveness", "eventId", eventID, "staled", res.Staled, "ended", res.Ended)
	}
	return true, nil
}
