package delivery

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"
	pkgtelemetry "github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/telemetry"

	mock_outbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/_mock/queue/port/outbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/infrastructure/telemetry"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/infrastructure/worker"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

func TestPromoterTuningFromConfig(t *testing.T) {
	cfg := testConfig(t)
	cfg.WorkerTuning.PromoterLeaseTTL = 7 * time.Second
	cfg.WorkerTuning.PromoteBatchMax = 33
	cfg.WorkerTuning.UnsealFailMax = 2
	cfg.WorkerTuning.UnsealProbe = 4
	cfg.WorkerTuning.NoSupplySuspectTicks = 5
	assert.Equal(t, worker.PromoterTuning{BatchMax: 33, LeaseTTL: 7 * time.Second, UnsealFailMax: 2, UnsealProbe: 4, NoSupplySuspectTicks: 5}, promoterTuning(cfg))
}

func TestSweepBatchFromConfig(t *testing.T) {
	cfg := testConfig(t)
	cfg.WorkerTuning.SweepBatchMax = 42
	assert.Equal(t, 42, sweepBatch(cfg))
}

func TestJoinSettingsFromConfig(t *testing.T) {
	cfg := testConfig(t)
	cfg.WorkerTuning.PromoterLeaseTTL = 7 * time.Second
	js := joinSettings(cfg)
	assert.Equal(t, 7*time.Second, js.AdmissionLeaseTTL)
	assert.Equal(t, cfg.RateLimit.JoinPerEmail, js.JoinPerEmail)
	assert.Equal(t, cfg.RateLimit.JoinPerIPWindow, js.JoinPerIPWindow)
	assert.Equal(t, cfg.Token.AdmissionTTL, js.ClaimWindow)
}

func TestPromoterUsesConfiguredLeaseTTL(t *testing.T) {
	cfg := testConfig(t)
	cfg.WorkerTuning.PromoterLeaseTTL = 7 * time.Second
	ctrl := gomock.NewController(t)
	store := mock_outbound.NewMockIQueueStore(ctrl)
	settings := mock_outbound.NewMockIEventSettingsRepo(ctrl)
	settings.EXPECT().ListEventIDs(gomock.Any()).Return([]string{"ev1"}, nil)
	store.EXPECT().AcquirePromoterLease(gomock.Any(), "ev1", 7*time.Second).Return("", false, nil)

	p := worker.NewPromoter(store, settings, nil, nil, nil, nil, clock.System{}, nil, promoterTuning(cfg))
	require.NoError(t, p.Run(context.Background()))
}

func TestSweeperUsesConfiguredBatch(t *testing.T) {
	cfg := testConfig(t)
	cfg.WorkerTuning.SweepBatchMax = 42
	ctrl := gomock.NewController(t)
	store := mock_outbound.NewMockIQueueStore(ctrl)
	settings := mock_outbound.NewMockIEventSettingsRepo(ctrl)
	settings.EXPECT().ListEventIDs(gomock.Any()).Return([]string{"ev1"}, nil)
	store.EXPECT().ExpiredActive(gomock.Any(), "ev1", gomock.Any(), 42).Return(nil, nil)

	s := worker.NewSweeper(store, settings, clock.System{}, sweepBatch(cfg))
	require.NoError(t, s.Run(context.Background()))
}

// (5) UnsealFailMax from config reaches the promoter: with 2 the entry is ended on the second failed tick count.
func TestPromoterUsesConfiguredUnsealFailMax(t *testing.T) {
	for _, tc := range []struct {
		failMax   int
		wantEnded int
	}{{2, 1}, {3, 0}} {
		cfg := testConfig(t)
		cfg.WorkerTuning.UnsealFailMax = tc.failMax
		ctrl := gomock.NewController(t)
		store := mock_outbound.NewMockIQueueStore(ctrl)
		settings := mock_outbound.NewMockIEventSettingsRepo(ctrl)
		booking := mock_outbound.NewMockIBookingClient(ctrl)
		pii := mock_outbound.NewMockIPIIProtector(ctrl)
		settings.EXPECT().ListEventIDs(gomock.Any()).Return([]string{"ev1"}, nil)
		store.EXPECT().AcquirePromoterLease(gomock.Any(), "ev1", gomock.Any()).Return("tok", true, nil)
		store.EXPECT().ReleasePromoterLease(gomock.Any(), "ev1", "tok").Return(nil)
		settings.EXPECT().Get(gomock.Any(), "ev1").Return(entity.EventSettings{ActiveSlotCapacity: 2}, true, nil)
		store.EXPECT().ActiveCount(gomock.Any(), "ev1").Return(0, nil)
		store.EXPECT().TopWaiting(gomock.Any(), "ev1", 2).Return([]string{"H", "E2"}, nil)
		store.EXPECT().GetEntry(gomock.Any(), "ev1", "H").Return(entity.Entry{ID: "H", EmailEnc: []byte("bad")}, true, nil)
		store.EXPECT().GetEntry(gomock.Any(), "ev1", "E2").Return(entity.Entry{ID: "E2", EmailEnc: []byte("ok"), VisitorID: "v2"}, true, nil)
		pii.EXPECT().UnsealEmail([]byte("bad")).Return("", assert.AnError)
		pii.EXPECT().UnsealEmail([]byte("ok")).Return("x@y.id", nil)
		pii.EXPECT().SealBuyerEmail("x@y.id").Return("c2VhbGVk", nil)
		store.EXPECT().NoteUnreadable(gomock.Any(), "ev1", "H").Return(2, nil)
		booking.EXPECT().ReserveBatch(gomock.Any(), gomock.Any()).Return(outbound.AllocationBatchResponse{
			Allocations: []outbound.AllocationResult{{QueueEntryID: "E2", Status: "NO_SUPPLY_NOW"}},
		}, nil)
		if tc.wantEnded == 1 {
			store.EXPECT().EndEntry(gomock.Any(), "ev1", "H", outbound.EndUnreadablePII, gomock.Any()).Return(outbound.EndResult{Tag: "OK"}, nil)
		}
		p := worker.NewPromoter(store, settings, booking, nil, nil, pii, clock.System{}, nil, promoterTuning(cfg))
		require.NoError(t, p.Run(context.Background()), "failMax=%d", tc.failMax)
	}
}

func scrape(t *testing.T, reg *pkgtelemetry.Registry) string {
	t.Helper()
	rec := httptest.NewRecorder()
	reg.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	return rec.Body.String()
}

// (6) lapsed-replay and ALREADY_ACTIVE counters reach the exposition through the wired telemetry adapter.
func TestPromoterWiredMetricsShowLapsedReplayAndDeadHead(t *testing.T) {
	cfg := testConfig(t)
	now := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	reg := pkgtelemetry.New()
	metrics := telemetry.NewPromoterMetrics(reg) // once per registry: names are registered exactly once

	run := func(reply outbound.AllocationResult, expectEnd bool) {
		ctrl := gomock.NewController(t)
		store := mock_outbound.NewMockIQueueStore(ctrl)
		settings := mock_outbound.NewMockIEventSettingsRepo(ctrl)
		booking := mock_outbound.NewMockIBookingClient(ctrl)
		pii := mock_outbound.NewMockIPIIProtector(ctrl)
		settings.EXPECT().ListEventIDs(gomock.Any()).Return([]string{"ev1"}, nil)
		store.EXPECT().AcquirePromoterLease(gomock.Any(), "ev1", gomock.Any()).Return("tok", true, nil)
		store.EXPECT().ReleasePromoterLease(gomock.Any(), "ev1", "tok").Return(nil)
		settings.EXPECT().Get(gomock.Any(), "ev1").Return(entity.EventSettings{ActiveSlotCapacity: 1}, true, nil)
		store.EXPECT().ActiveCount(gomock.Any(), "ev1").Return(0, nil)
		store.EXPECT().TopWaiting(gomock.Any(), "ev1", 1).Return([]string{"E1"}, nil)
		store.EXPECT().GetEntry(gomock.Any(), "ev1", "E1").Return(entity.Entry{ID: "E1", EmailEnc: []byte("ok")}, true, nil)
		pii.EXPECT().UnsealEmail(gomock.Any()).Return("x@y.id", nil)
		pii.EXPECT().SealBuyerEmail("x@y.id").Return("c2VhbGVk", nil)
		booking.EXPECT().ReserveBatch(gomock.Any(), gomock.Any()).Return(outbound.AllocationBatchResponse{Allocations: []outbound.AllocationResult{reply}}, nil)
		if expectEnd {
			store.EXPECT().EndEntry(gomock.Any(), "ev1", "E1", outbound.EndAlreadyActive, gomock.Any()).Return(outbound.EndResult{Tag: "OK"}, nil)
		}
		// no Promote, no ReleaseAllocation: gomock fails on either
		p := worker.NewPromoter(store, settings, booking, nil, nil, pii, clock.Fixed{T: now}, nil, promoterTuning(cfg)).
			WithMetrics(metrics)
		require.NoError(t, p.Run(context.Background()))
	}
	run(outbound.AllocationResult{QueueEntryID: "E1", AllocationID: "a", Status: "GRANTED", ClaimDeadline: now.Add(-time.Second).Format(time.RFC3339)}, false)
	run(outbound.AllocationResult{QueueEntryID: "E1", Status: "ALREADY_ACTIVE"}, true)

	out := scrape(t, reg)
	assert.Contains(t, out, "queue_promote_lapsed_replay_skipped_total 1")
	assert.Contains(t, out, `queue_entry_ended_total{outcome="ALREADY_ACTIVE"} 1`)
}

// (8) NoSupplySuspectTicks from config reaches the head detector: with N=5 the repeat series appears on tick 5.
func TestPromoterUsesConfiguredNoSupplySuspectTicks(t *testing.T) {
	for _, tc := range []struct {
		n          int
		wantSeries bool
	}{{5, true}, {6, false}} {
		cfg := testConfig(t)
		cfg.WorkerTuning.NoSupplySuspectTicks = tc.n
		reg := pkgtelemetry.New()
		ctrl := gomock.NewController(t)
		store := mock_outbound.NewMockIQueueStore(ctrl)
		settings := mock_outbound.NewMockIEventSettingsRepo(ctrl)
		booking := mock_outbound.NewMockIBookingClient(ctrl)
		pii := mock_outbound.NewMockIPIIProtector(ctrl)
		pub := mock_outbound.NewMockIEventPublisher(ctrl)
		settings.EXPECT().ListEventIDs(gomock.Any()).Return([]string{"ev1"}, nil).Times(5)
		settings.EXPECT().Get(gomock.Any(), "ev1").Return(entity.EventSettings{ActiveSlotCapacity: 2}, true, nil).Times(5)
		store.EXPECT().AcquirePromoterLease(gomock.Any(), "ev1", gomock.Any()).Return("tok", true, nil).Times(5)
		store.EXPECT().ReleasePromoterLease(gomock.Any(), "ev1", "tok").Return(nil).Times(5)
		store.EXPECT().ActiveCount(gomock.Any(), "ev1").Return(0, nil).Times(5)
		store.EXPECT().TopWaiting(gomock.Any(), "ev1", 2).Return([]string{"P", "E2"}, nil).Times(5)
		store.EXPECT().GetEntry(gomock.Any(), "ev1", gomock.Any()).Return(entity.Entry{EmailEnc: []byte("ok"), VisitorID: "v"}, true, nil).Times(10)
		pii.EXPECT().UnsealEmail(gomock.Any()).Return("x@y.id", nil).Times(10)
		pii.EXPECT().SealBuyerEmail("x@y.id").Return("c2VhbGVk", nil).Times(10)
		booking.EXPECT().ReserveBatch(gomock.Any(), gomock.Any()).Return(outbound.AllocationBatchResponse{Allocations: []outbound.AllocationResult{
			{QueueEntryID: "P", Status: "NO_SUPPLY_NOW"},
			{QueueEntryID: "E2", AllocationID: "a2", Status: "GRANTED", ClaimDeadline: "2099-01-01T00:00:00Z"},
		}}, nil).Times(5)
		store.EXPECT().Promote(gomock.Any(), "ev1", gomock.Any(), 2, gomock.Any(), gomock.Any()).Return(outbound.PromoteResult{Promoted: []string{"E2"}}, nil).Times(5)
		pub.EXPECT().PublishTurnArrived(gomock.Any(), "ev1", "E2", gomock.Any(), gomock.Any()).Return(nil).Times(5)

		p := worker.NewPromoter(store, settings, booking, pub, nil, pii, clock.Fixed{T: time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)}, nil, promoterTuning(cfg)).
			WithMetrics(telemetry.NewPromoterMetrics(reg))
		for i := 1; i <= 5; i++ {
			require.NoError(t, p.Run(context.Background()))
			out := scrape(t, reg)
			if i < 5 {
				assert.NotContains(t, out, "queue_promote_head_no_supply_repeat_total", "N=%d tick %d", tc.n, i)
			}
		}
		out := scrape(t, reg)
		if tc.wantSeries {
			assert.Contains(t, out, `queue_promote_head_no_supply_repeat_total{adopted="true"} 1`, "N=%d", tc.n)
		} else {
			assert.NotContains(t, out, "queue_promote_head_no_supply_repeat_total", "N=%d", tc.n)
		}
	}
}
