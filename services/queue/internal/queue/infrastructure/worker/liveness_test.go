package worker_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/infrastructure/worker"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

func newLivenessForTest(m workerMocks) (*worker.Liveness, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	l := worker.NewLiveness(m.store, m.settings, clock.System{}, worker.LivenessTuning{
		StaleAfter: 90 * time.Second, EndAfter: 300 * time.Second, BatchMax: 10, LeaseTTL: 10 * time.Second,
	}).WithLogger(slog.New(slog.NewTextHandler(buf, nil)))
	return l, buf
}

func warnCount(buf *bytes.Buffer) int {
	return strings.Count(buf.String(), "liveness lease held, sweep skipped")
}

// TC-QUE-33 (P9/P11): a held lease is a silent skip (nil, no state change); a Warn is logged every 6th consecutive
// skip per event and the counter resets after a successful pass.
func TestLiveness_LeaseHeldSkipsAndWarnsAfterSixConsecutive(t *testing.T) {
	m := newWorkerMocks(t)
	l, buf := newLivenessForTest(m)
	ctx := context.Background()
	m.settings.EXPECT().ListEventIDs(gomock.Any()).Return([]string{"ev1", "ev2"}, nil).AnyTimes()

	// ev1 is always held; ev2 is held only on the first pass, then succeeds.
	m.store.EXPECT().SweepLivenessTick(gomock.Any(), "ev1", gomock.Any(), gomock.Any(), 10, gomock.Any(), 10*time.Second, 0, gomock.Any()).
		Return(outbound.LivenessResult{LeaseLost: true}, nil, nil).AnyTimes()
	m.store.EXPECT().SweepLivenessTick(gomock.Any(), "ev2", gomock.Any(), gomock.Any(), 10, gomock.Any(), 10*time.Second, 0, gomock.Any()).
		Return(outbound.LivenessResult{LeaseLost: true}, nil, nil).Times(1)
	m.store.EXPECT().SweepLivenessTick(gomock.Any(), "ev2", gomock.Any(), gomock.Any(), 10, gomock.Any(), 10*time.Second, 0, gomock.Any()).
		Return(outbound.LivenessResult{}, nil, nil).AnyTimes()

	for i := 1; i <= 5; i++ {
		require.NoError(t, l.Run(ctx))
		assert.Zero(t, warnCount(buf), "no warning before the 6th consecutive skip (pass %d)", i)
	}
	require.NoError(t, l.Run(ctx)) // 6th
	assert.Equal(t, 1, warnCount(buf))
	assert.Contains(t, buf.String(), "ev1")
	assert.NotContains(t, buf.String(), "ev2", "ev2 was reset by a successful pass")

	for i := 7; i <= 11; i++ {
		require.NoError(t, l.Run(ctx))
	}
	assert.Equal(t, 1, warnCount(buf))
	require.NoError(t, l.Run(ctx)) // 12th
	assert.Equal(t, 2, warnCount(buf))
}

func TestLiveness_SuccessResetsCounter(t *testing.T) {
	m := newWorkerMocks(t)
	l, buf := newLivenessForTest(m)
	ctx := context.Background()
	m.settings.EXPECT().ListEventIDs(gomock.Any()).Return([]string{"ev1"}, nil).AnyTimes()
	held := outbound.LivenessResult{LeaseLost: true}
	seq := []outbound.LivenessResult{held, held, held, held, held, {}, held, held, held, held, held, held}
	i := 0
	m.store.EXPECT().SweepLivenessTick(gomock.Any(), "ev1", gomock.Any(), gomock.Any(), 10, gomock.Any(), 10*time.Second, 0, gomock.Any()).
		DoAndReturn(func(context.Context, string, int64, int64, int, int64, time.Duration, int, int) (outbound.LivenessResult, []string, error) {
			r := seq[i]
			i++
			return r, nil, nil
		}).Times(len(seq))
	for range seq[:11] {
		require.NoError(t, l.Run(ctx))
	}
	assert.Zero(t, warnCount(buf), "5 held, success, 5 held: never 6 in a row")
	require.NoError(t, l.Run(ctx))
	assert.Equal(t, 1, warnCount(buf))
}

// Invariant #2 / QWC-04.3: with the lease held by another holder, a promoter tick calls neither Promote nor Booking.
func TestPromoter_LeaseHeldMakesNoPromoteOrReserve(t *testing.T) {
	m := newWorkerMocks(t)
	m.settings.EXPECT().ListEventIDs(gomock.Any()).Return([]string{"ev1"}, nil).AnyTimes()
	m.store.EXPECT().AcquirePromoterLease(gomock.Any(), "ev1", 10*time.Second).Return("", false, nil).Times(20)
	p := worker.NewPromoter(m.store, m.settings, m.booking, m.pub, m.audit, clock.System{}, nil,
		worker.PromoterTuning{BatchMax: 10, LeaseTTL: 10 * time.Second, NoSupplySuspectTicks: 50})
	for range 20 {
		require.NoError(t, p.Run(context.Background()))
	}
}

// Invariant #2 / QWC-04.5: at capacity (free == 0) 50 promoter ticks make no Booking reservation.
func TestPromoter_NoFreeSlotMakesNoReserveBatch(t *testing.T) {
	m := newWorkerMocks(t)
	m.settings.EXPECT().ListEventIDs(gomock.Any()).Return([]string{"ev1"}, nil).AnyTimes()
	m.store.EXPECT().AcquirePromoterLease(gomock.Any(), "ev1", 10*time.Second).Return("1", true, nil).Times(50)
	m.store.EXPECT().ReleasePromoterLease(gomock.Any(), "ev1", "1").Return(nil).Times(50)
	m.settings.EXPECT().Get(gomock.Any(), "ev1").Return(entity.EventSettings{ActiveSlotCapacity: 3}, true, nil).AnyTimes()
	m.store.EXPECT().ActiveCount(gomock.Any(), "ev1").Return(3, nil).Times(50)
	// No TopWaiting/ReserveBatch/Promote expectations: gomock fails the test on any such call.
	p := worker.NewPromoter(m.store, m.settings, m.booking, m.pub, m.audit, clock.System{}, nil,
		worker.PromoterTuning{BatchMax: 10, LeaseTTL: 10 * time.Second, NoSupplySuspectTicks: 50})
	for range 50 {
		require.NoError(t, p.Run(context.Background()))
	}
}

// Regression: counters of events that left the settings list are evicted, so a re-added event restarts at zero.
func TestLiveness_EvictsCounterOfRemovedEvent(t *testing.T) {
	m := newWorkerMocks(t)
	l, buf := newLivenessForTest(m)
	ctx := context.Background()
	gomock.InOrder(
		m.settings.EXPECT().ListEventIDs(gomock.Any()).Return([]string{"ev1"}, nil).Times(5),
		m.settings.EXPECT().ListEventIDs(gomock.Any()).Return([]string{}, nil).Times(1),
		m.settings.EXPECT().ListEventIDs(gomock.Any()).Return([]string{"ev1"}, nil).Times(1),
	)
	m.store.EXPECT().SweepLivenessTick(gomock.Any(), "ev1", gomock.Any(), gomock.Any(), 10, gomock.Any(), 10*time.Second, 0, gomock.Any()).
		Return(outbound.LivenessResult{LeaseLost: true}, nil, nil).AnyTimes()

	for i := 0; i < 7; i++ {
		require.NoError(t, l.Run(ctx))
	}
	assert.Zero(t, warnCount(buf), "6th lost pass overall, but the counter was evicted while ev1 was absent")
}

// C8.7 (PF-P2-1): the start shard rotates once per tick, shared by every event of that tick, so each tick starts its
// budget at the next shard and the shards a budget misses are reached by later ticks.
func TestLiveness_StartShardRotatesPerTick(t *testing.T) {
	m := newWorkerMocks(t)
	l, _ := newLivenessForTest(m)
	ctx := context.Background()
	m.settings.EXPECT().ListEventIDs(gomock.Any()).Return([]string{"ev1", "ev2"}, nil).AnyTimes()
	var starts []int
	m.store.EXPECT().SweepLivenessTick(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), 10, gomock.Any(), 10*time.Second, 0, gomock.Any()).
		DoAndReturn(func(_ context.Context, ev string, _, _ int64, _ int, _ int64, _ time.Duration, _ int, start int) (outbound.LivenessResult, []string, error) {
			if ev == "ev1" {
				starts = append(starts, start)
			}
			return outbound.LivenessResult{}, nil, nil
		}).AnyTimes()

	for i := 0; i < 3; i++ {
		require.NoError(t, l.Run(ctx))
	}
	assert.Equal(t, []int{0, 1, 2}, starts, "tick i starts at shard i; ev2 shares the tick's start")
}
