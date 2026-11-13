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
	cfg.WorkerTuning.NoSupplySuspectTicks = 5
	assert.Equal(t, worker.PromoterTuning{BatchMax: 33, LeaseTTL: 7 * time.Second, NoSupplySuspectTicks: 5}, promoterTuning(cfg))
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
	assert.Equal(t, cfg.RateLimit.JoinPerIPWindow, js.JoinPerIPWindow)
	assert.Equal(t, cfg.Token.AdmissionTTL, js.ClaimWindow)
}

func TestPromoterUsesConfiguredLeaseTTL(t *testing.T) {
	cfg := testConfig(t)
	cfg.WorkerTuning.PromoterLeaseTTL = 7 * time.Second
	ctrl := gomock.NewController(t)
	store := mock_outbound.NewMockIQueueStore(ctrl)
	settings := mock_outbound.NewMockIEventSettingsRepo(ctrl)
	settings.EXPECT().ListActiveEventIDs(gomock.Any(), gomock.Any()).Return([]string{"ev1"}, nil)
	store.EXPECT().AcquirePromoterLease(gomock.Any(), "ev1", 7*time.Second).Return("", false, nil)

	p := worker.NewPromoter(store, settings, nil, nil, nil, clock.System{}, nil, promoterTuning(cfg))
	require.NoError(t, p.Run(context.Background()))
}

func TestSweeperUsesConfiguredBatch(t *testing.T) {
	cfg := testConfig(t)
	cfg.WorkerTuning.SweepBatchMax = 42
	ctrl := gomock.NewController(t)
	store := mock_outbound.NewMockIQueueStore(ctrl)
	settings := mock_outbound.NewMockIEventSettingsRepo(ctrl)
	settings.EXPECT().ListActiveEventIDs(gomock.Any(), gomock.Any()).Return([]string{"ev1"}, nil)
	store.EXPECT().ExpiredActive(gomock.Any(), "ev1", gomock.Any(), 42).Return(nil, nil)
	store.EXPECT().GetSale(gomock.Any(), "ev1").Return(outbound.SaleState{SaleState: "OPEN"}, nil)

	s := worker.NewSweeper(store, settings, clock.System{}, sweepBatch(cfg))
	require.NoError(t, s.Run(context.Background()))
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
		settings.EXPECT().ListActiveEventIDs(gomock.Any(), gomock.Any()).Return([]string{"ev1"}, nil)
		store.EXPECT().AcquirePromoterLease(gomock.Any(), "ev1", gomock.Any()).Return("tok", true, nil)
		store.EXPECT().ReleasePromoterLease(gomock.Any(), "ev1", "tok").Return(nil)
		settings.EXPECT().Get(gomock.Any(), "ev1").Return(entity.EventSettings{ActiveSlotCapacity: 1}, true, nil)
		store.EXPECT().ActiveCount(gomock.Any(), "ev1").Return(0, nil)
		store.EXPECT().TopWaiting(gomock.Any(), "ev1", 1).Return([]string{"E1"}, nil)
		store.EXPECT().GetEntries(gomock.Any(), "ev1", []string{"E1"}).Return(map[string]entity.Entry{"E1": {ID: "E1"}}, nil)
		booking.EXPECT().ReserveBatch(gomock.Any(), gomock.Any()).Return(outbound.AllocationBatchResponse{Allocations: []outbound.AllocationResult{reply}}, nil)
		if expectEnd {
			store.EXPECT().EndEntry(gomock.Any(), "ev1", "E1", outbound.EndAlreadyActive, gomock.Any()).Return(outbound.EndResult{Tag: "OK"}, nil)
		}
		// no Promote, no ReleaseAllocation: gomock fails on either
		p := worker.NewPromoter(store, settings, booking, nil, nil, clock.Fixed{T: now}, nil, promoterTuning(cfg)).
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
		pub := mock_outbound.NewMockIEventPublisher(ctrl)
		settings.EXPECT().ListActiveEventIDs(gomock.Any(), gomock.Any()).Return([]string{"ev1"}, nil).Times(5)
		settings.EXPECT().Get(gomock.Any(), "ev1").Return(entity.EventSettings{ActiveSlotCapacity: 2}, true, nil).Times(5)
		store.EXPECT().AcquirePromoterLease(gomock.Any(), "ev1", gomock.Any()).Return("tok", true, nil).Times(5)
		store.EXPECT().ReleasePromoterLease(gomock.Any(), "ev1", "tok").Return(nil).Times(5)
		store.EXPECT().ActiveCount(gomock.Any(), "ev1").Return(0, nil).Times(5)
		store.EXPECT().TopWaiting(gomock.Any(), "ev1", 2).Return([]string{"P", "E2"}, nil).Times(5)
		store.EXPECT().GetEntries(gomock.Any(), "ev1", []string{"P", "E2"}).Return(map[string]entity.Entry{"P": {VisitorID: "v"}, "E2": {VisitorID: "v"}}, nil).Times(5)
		booking.EXPECT().ReserveBatch(gomock.Any(), gomock.Any()).Return(outbound.AllocationBatchResponse{Allocations: []outbound.AllocationResult{
			{QueueEntryID: "P", Status: "NO_SUPPLY_NOW"},
			{QueueEntryID: "E2", AllocationID: "a2", Status: "GRANTED", ClaimDeadline: "2099-01-01T00:00:00Z"},
		}}, nil).Times(5)
		store.EXPECT().Promote(gomock.Any(), "ev1", gomock.Any(), 2, gomock.Any(), gomock.Any()).Return(outbound.PromoteResult{Promoted: []string{"E2"}}, nil).Times(5)
		pub.EXPECT().PublishTurnArrived(gomock.Any(), "ev1", "E2", gomock.Any(), gomock.Any()).Return(nil).Times(5)

		p := worker.NewPromoter(store, settings, booking, pub, nil, clock.Fixed{T: time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)}, nil, promoterTuning(cfg)).
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

// ADR-030 wiring: heartbeat interval, liveness thresholds (sharing the promoter lease TTL) and the visitor token TTL
// reach their consumers from config.
func TestAnonymousQueueWiringFromConfig(t *testing.T) {
	cfg := testConfig(t)
	cfg.Liveness.HeartbeatInterval, cfg.Liveness.NearInterval = 30*time.Second, 10*time.Second
	cfg.Liveness.StaleAfter, cfg.Liveness.EndAfter, cfg.Liveness.BatchMax = 90*time.Second, 300*time.Second, 77
	cfg.WorkerTuning.PromoterLeaseTTL = 7 * time.Second
	cfg.Token.VisitorTTL = 6 * time.Hour
	hp := heartbeatPolicy(cfg)
	assert.EqualValues(t, 30_000, hp.BaseMs)
	assert.EqualValues(t, 10_000, hp.NearMs)
	assert.Equal(t, worker.LivenessTuning{StaleAfter: 90 * time.Second, EndAfter: 300 * time.Second, BatchMax: 77, LeaseTTL: 7 * time.Second}, livenessTuning(cfg))
	assert.Equal(t, 6*time.Hour, joinSettings(cfg).VisitorTokenTTL)
}

// TC-QUE-32 P8: status TTL follows the status publisher's own cadence, not WORKER_INTERVAL.
func TestStatusTTL_UsesStatusInterval(t *testing.T) {
	cfg := testConfig(t)
	cfg.WorkerTuning.StatusInterval = time.Second
	cfg.Worker.Interval = 5 * time.Second
	cfg.WorkerTuning.StatusTTLMultiplier = 4
	assert.Equal(t, 4*time.Second, statusTTL(cfg), "must not be 4 x WORKER_INTERVAL (20s)")

	cfg.WorkerTuning.StatusInterval = 2 * time.Second
	assert.Equal(t, 8*time.Second, statusTTL(cfg))
}

func TestJobEvery_MapsEachJobToItsInterval(t *testing.T) {
	cfg := testConfig(t)
	cfg.WorkerTuning.PromotorInterval = 100 * time.Millisecond
	cfg.WorkerTuning.SweeperInterval = 10 * time.Second
	cfg.WorkerTuning.StatusInterval = time.Second
	cfg.Worker.Interval = 5 * time.Second
	assert.Equal(t, map[string]time.Duration{
		"promoter":         100 * time.Millisecond,
		"sweeper":          10 * time.Second,
		"status-publisher": time.Second,
		"liveness":         5 * time.Second,
	}, jobEvery(cfg))
}

func TestBuildJobs_CarryOwnInterval(t *testing.T) {
	cfg := testConfig(t)
	cfg.Worker.Interval = 5 * time.Second
	run := func(context.Context) error { return nil }
	js := buildJobs(cfg, run, run, run, run)
	got := map[string]time.Duration{}
	for _, j := range js {
		got[j.Name] = j.Every
		assert.NotNil(t, j.Run, j.Name)
	}
	assert.Equal(t, jobEvery(cfg), got)
	assert.Len(t, js, 4)
}

// QWC-03.4 / invariant #4: the admission token TTL default is unchanged and bounded.
func TestAdmissionTTL_UnchangedAndBounded(t *testing.T) {
	cfg := testConfig(t)
	assert.Equal(t, 60*time.Second, cfg.Token.AdmissionTTL)
	cfg.Token.AdmissionTTL = 6 * time.Minute
	assert.Error(t, cfg.Token.Validate())
}

// C8.12: Close is nil-safe and idempotent; a module without a publisher or connection closes cleanly.
func TestQueueModule_CloseIsNilSafe(t *testing.T) {
	var nilMod *queueModule
	assert.NoError(t, nilMod.Close())
	assert.NoError(t, (&queueModule{}).Close())

	m := &queueModule{}
	assert.NoError(t, m.Close())
	assert.NoError(t, m.Close(), "second Close must be a no-op")
}
