//go:build integration

package worker_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
	queuevalkey "github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/infrastructure/valkey"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/infrastructure/worker"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// fifoBooking reports salesStartsAt and grants everything (stock is not under test here). The opening-window
// draw was dropped (ADR-030 Amendment); all joins are strict FIFO by seq.
type fifoBooking struct {
	*bookingStub
	start time.Time
}

func (w fifoBooking) Supply(context.Context, string) (outbound.SupplySnapshot, error) {
	return outbound.SupplySnapshot{SalesStartsAt: w.start, MaxTicketsPerOrder: 3}, nil
}

// fifoEnv builds a FIFO promoter (no draw gate) over the real store for sweeper/promoter integration tests.
func fifoEnv(t *testing.T) (*queuevalkey.QueueStore, *clock.Fixed, *worker.Promoter, *fakeSettings, time.Time) {
	t.Helper()
	store, _ := twoStores(t)
	start := time.Date(2030, 1, 1, 10, 0, 0, 0, time.UTC)
	clk := &clock.Fixed{T: start}
	settings := &fakeSettings{s: entity.EventSettings{EventID: "ev", ActiveSlotCapacity: 1000, OneActivePerVisitor: true}}
	bk := fifoBooking{bookingStub: newBookingStub(clk, 3*time.Minute, 100000), start: start}
	p := worker.NewPromoter(store, settings, bk, &fakePub{}, itAudit{}, clk, nil, worker.PromoterTuning{BatchMax: 1000})
	return store, clk, p, settings, start
}

// R03 at the worker: the liveness sweeper marks silent entries STALE under the lease and the promoter skips them.
func TestLivenessSweeper_StaleEntriesAreNotAdmitted(t *testing.T) {
	store, clk, p, settings, start := fifoEnv(t)
	ctx := context.Background()
	settings.s.ActiveSlotCapacity = 10
	for i := 0; i < 3; i++ {
		_, err := store.Join(ctx, outbound.JoinParams{EventID: "ev", EntryID: fmt.Sprintf("e%d", i), NewVisitorID: fmt.Sprintf("v%d", i),
			NowMs: start.UnixMilli(), OneActivePerVisitor: true, TokenTTL: time.Hour})
		require.NoError(t, err)
	}
	clk.T = start.Add(60 * time.Second)
	_, err := store.Heartbeat(ctx, "ev", "v1", 1, clk.T.UnixMilli())
	require.NoError(t, err)
	clk.T = start.Add(91 * time.Second)
	lv := worker.NewLiveness(store, settings, clk, worker.LivenessTuning{StaleAfter: 90 * time.Second, EndAfter: 300 * time.Second, BatchMax: 100, LeaseTTL: 5 * time.Second})
	require.NoError(t, lv.Run(ctx))
	require.NoError(t, p.Run(ctx))
	for id, want := range map[string]string{"e0": "STALE", "e1": "ADMITTED", "e2": "STALE"} {
		e, _, _ := store.GetEntry(ctx, "ev", id)
		assert.Equal(t, want, string(e.Status), id)
	}
	clk.T = start.Add(301 * time.Second)
	require.NoError(t, lv.Run(ctx))
	e, _, _ := store.GetEntry(ctx, "ev", "e0")
	assert.Equal(t, "ENDED", string(e.Status))
	e, _, _ = store.GetEntry(ctx, "ev", "e1")
	assert.Equal(t, "ADMITTED", string(e.Status), "admitted entries never end by silence")
}

// inactiveSettings lists ev as configured but no longer active: its sale ended more than a day ago (D-C8.10).
type inactiveSettings struct{ fakeSettings }

func (f *inactiveSettings) ListActiveEventIDs(context.Context, time.Time) ([]string, error) {
	return nil, nil
}

// retireSpy records the answer of every RetireEvent call the real store gave.
type retireSpy struct {
	*queuevalkey.QueueStore
	answers []bool
}

func (s *retireSpy) RetireEvent(ctx context.Context, eventID string, ttl time.Duration) (bool, error) {
	ok, err := s.QueueStore.RetireEvent(ctx, eventID, ttl)
	if err == nil {
		s.answers = append(s.answers, ok)
	}
	return ok, err
}

// C8.10 Part B (R40, I1): an event past its cut-off is still iterated by liveness. An entry that keeps heartbeating is not
// ended and keeps the event from retiring; once it goes silent liveness ends it, and the event retires in the same tick. The
// retirement is remembered, so later ticks do not call RetireEvent again.
func TestLivenessSweeper_InactiveEventRetiresOnceQuiet(t *testing.T) {
	store, clk, _, _, start := fifoEnv(t)
	ctx := context.Background()
	spy := &retireSpy{QueueStore: store}
	settings := &inactiveSettings{fakeSettings: fakeSettings{s: entity.EventSettings{EventID: "ev"}}}
	lv := worker.NewLiveness(spy, settings, clk, worker.LivenessTuning{StaleAfter: 90 * time.Second, EndAfter: 300 * time.Second, BatchMax: 100, LeaseTTL: 5 * time.Second})
	_, err := store.Join(ctx, outbound.JoinParams{EventID: "ev", EntryID: "e0", NewVisitorID: "v0",
		NowMs: start.UnixMilli(), OneActivePerVisitor: true, TokenTTL: time.Hour})
	require.NoError(t, err)

	for i := 1; i <= 4; i++ {
		clk.T = start.Add(time.Duration(60*i) * time.Second)
		_, err := store.Heartbeat(ctx, "ev", "v0", 1, clk.T.UnixMilli())
		require.NoError(t, err)
		require.NoError(t, lv.Run(ctx))
	}
	e, _, _ := store.GetEntry(ctx, "ev", "e0")
	assert.Equal(t, "WAITING", string(e.Status), "a heartbeating entry is not ended past the cut-off")
	assert.Equal(t, []bool{false, false, false, false}, spy.answers, "retirement is refused while the entry is live")

	clk.T = start.Add(240*time.Second + 301*time.Second) // 301 s after the last heartbeat
	require.NoError(t, lv.Run(ctx))
	e, _, _ = store.GetEntry(ctx, "ev", "e0")
	assert.Equal(t, "ENDED", string(e.Status))
	assert.Equal(t, []bool{false, false, false, false, true}, spy.answers, "the event retires once nothing is live")

	clk.T = clk.T.Add(5 * time.Second)
	require.NoError(t, lv.Run(ctx))
	assert.Len(t, spy.answers, 5, "a retired event is not retired again")
}
