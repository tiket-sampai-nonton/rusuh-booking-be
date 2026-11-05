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
