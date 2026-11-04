package worker_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/infrastructure/worker"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// expect one reserved-and-granted candidate flow up to Promote, for the lease tests below.
func expectGrantedFlow(m workerMocks, token string, promoteFn func(context.Context, string, []outbound.PromoteCandidate, int, int64, outbound.PromoteOpts) (outbound.PromoteResult, error)) {
	m.settings.EXPECT().ListEventIDs(gomock.Any()).Return([]string{"ev1"}, nil)
	m.store.EXPECT().AcquirePromoterLease(gomock.Any(), "ev1", 10*time.Second).Return(token, true, nil)
	m.settings.EXPECT().Get(gomock.Any(), "ev1").Return(entity.EventSettings{ActiveSlotCapacity: 10}, true, nil)
	m.store.EXPECT().ActiveCount(gomock.Any(), "ev1").Return(0, nil)
	m.store.EXPECT().TopWaiting(gomock.Any(), "ev1", 10).Return([]string{"e1"}, nil)
	m.store.EXPECT().GetEntry(gomock.Any(), "ev1", "e1").Return(entity.Entry{ID: "e1", VisitorID: "v1", BuyerNikHash: []byte{0xaa}}, true, nil)
	m.booking.EXPECT().ReserveBatch(gomock.Any(), gomock.Any()).Return(outbound.AllocationBatchResponse{
		Allocations: []outbound.AllocationResult{{QueueEntryID: "e1", AllocationID: "a1", Status: "GRANTED", ClaimDeadline: "2026-10-20T10:01:00Z"}},
	}, nil)
	m.store.EXPECT().Promote(gomock.Any(), "ev1", gomock.Any(), 10, gomock.Any(), gomock.Any()).DoAndReturn(promoteFn)
}

// The token reaches Promote and the release; the release uses a context that survives cancellation of the tick.
func TestPromoter_TokenReachesPromoteAndReleaseSurvivesCancelledContext(t *testing.T) {
	m := newWorkerMocks(t)
	ctx, cancel := context.WithCancel(context.Background())
	expectGrantedFlow(m, "tok-1", func(_ context.Context, _ string, _ []outbound.PromoteCandidate, _ int, _ int64, opts outbound.PromoteOpts) (outbound.PromoteResult, error) {
		assert.Equal(t, "tok-1", opts.LeaseToken)
		assert.False(t, opts.RequireHead, "promoter memakai batch, bukan requireHead")
		cancel() // the caller goes away mid-tick
		return outbound.PromoteResult{Promoted: []string{"e1"}}, nil
	})
	m.pub.EXPECT().PublishTurnArrived(gomock.Any(), "ev1", "e1", "v1", gomock.Any()).Return(nil)
	m.store.EXPECT().ReleasePromoterLease(gomock.Any(), "ev1", "tok-1").DoAndReturn(func(rctx context.Context, _, _ string) error {
		assert.NoError(t, rctx.Err(), "pelepasan lease tidak ikut dibatalkan")
		return nil
	})
	p := worker.NewPromoter(m.store, m.settings, m.booking, m.pub, m.audit, m.pii, wnow, nil, worker.PromoterTuning{BatchMax: 200})
	require.NoError(t, p.Run(ctx))
}

// The tick is bounded to ttl-1s so a slow Booking cannot outlive the lease.
func TestPromoter_TickContextIsBoundedBelowTheLeaseTTL(t *testing.T) {
	m := newWorkerMocks(t)
	ctx := context.Background()
	start := time.Now()
	var deadline time.Time
	m.settings.EXPECT().ListEventIDs(gomock.Any()).Return([]string{"ev1"}, nil)
	m.store.EXPECT().AcquirePromoterLease(gomock.Any(), "ev1", 5*time.Second).Return("tok", true, nil)
	m.store.EXPECT().ReleasePromoterLease(gomock.Any(), "ev1", "tok").Return(nil)
	m.settings.EXPECT().Get(gomock.Any(), "ev1").DoAndReturn(func(c context.Context, _ string) (entity.EventSettings, bool, error) {
		d, ok := c.Deadline()
		require.True(t, ok, "tick harus punya deadline")
		deadline = d
		return entity.EventSettings{ActiveSlotCapacity: 1}, true, nil
	})
	m.store.EXPECT().ActiveCount(gomock.Any(), "ev1").Return(1, nil) // full: nothing more to do
	p := worker.NewPromoter(m.store, m.settings, m.booking, m.pub, m.audit, m.pii, wnow, nil, worker.PromoterTuning{BatchMax: 200, LeaseTTL: 5 * time.Second})
	require.NoError(t, p.Run(ctx))
	remaining := deadline.Sub(start)
	assert.InDelta(t, (4 * time.Second).Seconds(), remaining.Seconds(), 0.5, "deadline == ttl - 1s")
}

// LeaseLost: nothing is released, the counter moves, the tick ends quietly.
func TestPromoter_LeaseLostReleasesNothingAndCounts(t *testing.T) {
	m := newWorkerMocks(t)
	h := newFakeMetrics()
	ctx := context.Background()
	expectGrantedFlow(m, "tok", func(context.Context, string, []outbound.PromoteCandidate, int, int64, outbound.PromoteOpts) (outbound.PromoteResult, error) {
		return outbound.PromoteResult{LeaseLost: true}, nil
	})
	m.store.EXPECT().ReleasePromoterLease(gomock.Any(), "ev1", "tok").Return(nil)
	// no ReleaseAllocation, no PublishTurnArrived expectations
	p := worker.NewPromoter(m.store, m.settings, m.booking, m.pub, m.audit, m.pii, wnow, nil, worker.PromoterTuning{BatchMax: 200}).WithMetrics(h)
	require.NoError(t, p.Run(ctx))
	assert.Equal(t, 1, h.Count(worker.MetricLeaseLost))
}

// Defence in depth: FULL after a grant keeps the RESERVED row (no release) and raises the defect signal.
func TestPromoter_FullAfterGrantKeepsTheRowAndRaisesTheDefectSignal(t *testing.T) {
	m := newWorkerMocks(t)
	h := newFakeMetrics()
	expectGrantedFlow(m, "tok", func(context.Context, string, []outbound.PromoteCandidate, int, int64, outbound.PromoteOpts) (outbound.PromoteResult, error) {
		return outbound.PromoteResult{Full: true}, nil
	})
	m.store.EXPECT().ReleasePromoterLease(gomock.Any(), "ev1", "tok").Return(nil)
	p := worker.NewPromoter(m.store, m.settings, m.booking, m.pub, m.audit, m.pii, wnow, nil, worker.PromoterTuning{BatchMax: 200}).WithMetrics(h)
	require.NoError(t, p.Run(context.Background()))
	assert.Equal(t, 1, h.Count(worker.MetricFullAfterGrant))
}

// Terminal skip -> exactly one release of the GRANTED id; ADMITTED_SAME -> none; promoted -> none.
func TestPromoter_ReleasesOnlyScriptProvenTerminalSkips(t *testing.T) {
	for _, tc := range []struct {
		status      string
		wantRelease bool
	}{{"LEFT", true}, {"ENDED", true}, {"NO_ENTRY", true}, {"ADMITTED_OTHER", true}, {"ADMITTED_SAME", false}, {"NOT_HEAD", false}} {
		t.Run(tc.status, func(t *testing.T) {
			m := newWorkerMocks(t)
			expectGrantedFlow(m, "tok", func(context.Context, string, []outbound.PromoteCandidate, int, int64, outbound.PromoteOpts) (outbound.PromoteResult, error) {
				return outbound.PromoteResult{Skipped: []outbound.PromoteSkip{{EntryID: "e1", Status: tc.status}}}, nil
			})
			m.store.EXPECT().ReleasePromoterLease(gomock.Any(), "ev1", "tok").Return(nil)
			if tc.wantRelease {
				m.booking.EXPECT().ReleaseAllocation(gomock.Any(), "a1", "QUEUE_ENTRY_GONE").Return(nil)
			}
			p := worker.NewPromoter(m.store, m.settings, m.booking, m.pub, m.audit, m.pii, wnow, nil, worker.PromoterTuning{BatchMax: 200})
			require.NoError(t, p.Run(context.Background()))
		})
	}
}

// TC-QUE-27: one failing event never starves the others; the errors are joined.
func TestPromoter_OneEventsFailureDoesNotStopTheOthers(t *testing.T) {
	m := newWorkerMocks(t)
	m.settings.EXPECT().ListEventIDs(gomock.Any()).Return([]string{"ev1", "ev2"}, nil)
	m.store.EXPECT().AcquirePromoterLease(gomock.Any(), "ev1", gomock.Any()).Return("", false, errors.New("valkey down for ev1"))
	m.store.EXPECT().AcquirePromoterLease(gomock.Any(), "ev2", gomock.Any()).Return("t2", true, nil)
	m.store.EXPECT().ReleasePromoterLease(gomock.Any(), "ev2", "t2").Return(nil)
	m.settings.EXPECT().Get(gomock.Any(), "ev2").Return(entity.EventSettings{ActiveSlotCapacity: 1}, true, nil)
	m.store.EXPECT().ActiveCount(gomock.Any(), "ev2").Return(1, nil)
	p := worker.NewPromoter(m.store, m.settings, m.booking, m.pub, m.audit, m.pii, wnow, nil, worker.PromoterTuning{BatchMax: 200})
	err := p.Run(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ev1")
}

// TC-QUE-31: pre-turn notification is attempted on every return path after the lease, including "no candidates" and
// an early error; a notify failure never changes the tick result (INV-6).
func TestPromoter_NotifyRunsOnEveryReturnPathAfterTheLease(t *testing.T) {
	enabled := entity.EventSettings{ActiveSlotCapacity: 5, NearFrontMultiplier: 2, PreTurnNotifyEnabled: true}
	cases := map[string]func(m workerMocks) error{
		"no candidates": func(m workerMocks) error {
			m.store.EXPECT().ActiveCount(gomock.Any(), "ev1").Return(0, nil)
			m.store.EXPECT().TopWaiting(gomock.Any(), "ev1", 5).Return(nil, nil)
			return nil
		},
		"active count error": func(m workerMocks) error {
			m.store.EXPECT().ActiveCount(gomock.Any(), "ev1").Return(0, errors.New("boom"))
			return errors.New("boom")
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			m := newWorkerMocks(t)
			m.settings.EXPECT().ListEventIDs(gomock.Any()).Return([]string{"ev1"}, nil)
			m.store.EXPECT().AcquirePromoterLease(gomock.Any(), "ev1", gomock.Any()).Return("tok", true, nil)
			m.store.EXPECT().ReleasePromoterLease(gomock.Any(), "ev1", "tok").Return(nil)
			m.settings.EXPECT().Get(gomock.Any(), "ev1").Return(enabled, true, nil)
			wantErr := setup(m)
			// notify: reads the near-front window, finds one entry already notified => no publish
			m.store.EXPECT().TopWaiting(gomock.Any(), "ev1", 10).Return([]string{"n1"}, nil)
			m.store.EXPECT().WasNotifiedSoon(gomock.Any(), "ev1", "n1").Return(true, nil)
			p := worker.NewPromoter(m.store, m.settings, m.booking, m.pub, m.audit, m.pii, wnow, nil, worker.PromoterTuning{BatchMax: 200})
			err := p.Run(context.Background())
			if wantErr != nil {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
