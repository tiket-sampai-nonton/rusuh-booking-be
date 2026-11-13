package worker_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"

	mock_outbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/_mock/queue/port/outbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/infrastructure/worker"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

var wnow = clock.Fixed{T: time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)}

type workerMocks struct {
	store    *mock_outbound.MockIQueueStore
	settings *mock_outbound.MockIEventSettingsRepo
	booking  *mock_outbound.MockIBookingClient
	pub      *mock_outbound.MockIEventPublisher
	audit    *mock_outbound.MockIEntryAuditRepo
}

func newWorkerMocks(t *testing.T) workerMocks {
	t.Helper()
	ctrl := gomock.NewController(t)
	return workerMocks{
		store:    mock_outbound.NewMockIQueueStore(ctrl),
		settings: mock_outbound.NewMockIEventSettingsRepo(ctrl),
		booking:  mock_outbound.NewMockIBookingClient(ctrl),
		pub:      mock_outbound.NewMockIEventPublisher(ctrl),
		audit:    mock_outbound.NewMockIEntryAuditRepo(ctrl),
	}
}

// expectLease sets up the acquire+release expectations for tests that proceed past the lease check.
func expectLease(m workerMocks, ctx context.Context, eventID string) {
	m.store.EXPECT().AcquirePromoterLease(gomock.Any(), eventID, 10*time.Second).Return("1", true, nil)
	m.store.EXPECT().ReleasePromoterLease(gomock.Any(), eventID, "1").Return(nil)
}

func TestPromoter_PromotesReservedAndPublishes(t *testing.T) {
	m := newWorkerMocks(t)
	ctx := context.Background()

	m.settings.EXPECT().ListEventIDs(gomock.Any()).Return([]string{"ev1"}, nil)
	expectLease(m, ctx, "ev1")
	m.settings.EXPECT().Get(gomock.Any(), "ev1").Return(entity.EventSettings{ActiveSlotCapacity: 10}, true, nil)
	m.store.EXPECT().ActiveCount(gomock.Any(), "ev1").Return(0, nil)
	m.store.EXPECT().TopWaiting(gomock.Any(), "ev1", 10).Return([]string{"e1"}, nil)
	m.store.EXPECT().GetEntry(gomock.Any(), "ev1", "e1").Return(entity.Entry{
		ID: "e1", VisitorID: "v1", AccountID: "acc-1",
	}, true, nil)
	// ADR-030: the admission is anonymous. Booking receives the account (identity only) and no buyer fields; the
	// buyer NIK, masked NIK and e-ticket email are bound at hold.
	m.booking.EXPECT().ReserveBatch(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, req outbound.AllocationBatchRequest) (outbound.AllocationBatchResponse, error) {
			require.Len(t, req.Entries, 1)
			assert.Equal(t, "acc-1", req.Entries[0].AccountID)
			return outbound.AllocationBatchResponse{
				Allocations: []outbound.AllocationResult{{QueueEntryID: "e1", AllocationID: "a1", Status: "GRANTED", ClaimDeadline: "2026-10-20T10:01:00Z"}},
			}, nil
		})
	m.store.EXPECT().Promote(gomock.Any(), "ev1", gomock.Any(), 10, gomock.Any(), gomock.Any()).Return(outbound.PromoteResult{Promoted: []string{"e1"}}, nil)
	m.pub.EXPECT().PublishTurnArrived(gomock.Any(), "ev1", "e1", "v1", gomock.Any()).Return(nil)

	p := worker.NewPromoter(m.store, m.settings, m.booking, m.pub, m.audit, wnow, nil, worker.PromoterTuning{BatchMax: 200})
	require.NoError(t, p.Run(ctx))
}

func TestPromoter_SkipsEventWhenLeaseHeld(t *testing.T) {
	m := newWorkerMocks(t)
	ctx := context.Background()
	m.settings.EXPECT().ListEventIDs(gomock.Any()).Return([]string{"ev1"}, nil)
	// Lease already held by another instance — return false (spec 11.R3).
	m.store.EXPECT().AcquirePromoterLease(gomock.Any(), "ev1", 10*time.Second).Return("", false, nil)
	// ActiveCount, TopWaiting, ReserveBatch, Promote must NOT be called.

	p := worker.NewPromoter(m.store, m.settings, m.booking, m.pub, m.audit, wnow, nil, worker.PromoterTuning{BatchMax: 200})
	require.NoError(t, p.Run(ctx))
}

func TestPromoter_NoFreeSlotsSkips(t *testing.T) {
	m := newWorkerMocks(t)
	ctx := context.Background()
	m.settings.EXPECT().ListEventIDs(gomock.Any()).Return([]string{"ev1"}, nil)
	expectLease(m, ctx, "ev1")
	m.settings.EXPECT().Get(gomock.Any(), "ev1").Return(entity.EventSettings{ActiveSlotCapacity: 5}, true, nil)
	m.store.EXPECT().ActiveCount(gomock.Any(), "ev1").Return(5, nil)

	p := worker.NewPromoter(m.store, m.settings, m.booking, m.pub, m.audit, wnow, nil, worker.PromoterTuning{BatchMax: 200})
	require.NoError(t, p.Run(ctx))
}

// ADR-030: the promoter reserves 3 units per entry and sends NO buyer fields. A stale BUYER_QUOTA_EXHAUSTED outcome
// from an old Booking pod is an unknown outcome: it never ends a live buyer and never promotes.
func TestPromoter_AnonymousThreeUnitRequestAndStaleQuotaOutcomeIsInert(t *testing.T) {
	m := newWorkerMocks(t)
	ctx := context.Background()
	m.settings.EXPECT().ListEventIDs(gomock.Any()).Return([]string{"ev1"}, nil)
	expectLease(m, ctx, "ev1")
	m.settings.EXPECT().Get(gomock.Any(), "ev1").Return(entity.EventSettings{ActiveSlotCapacity: 10}, true, nil)
	m.store.EXPECT().ActiveCount(gomock.Any(), "ev1").Return(0, nil)
	m.store.EXPECT().TopWaiting(gomock.Any(), "ev1", 10).Return([]string{"e1"}, nil)
	m.store.EXPECT().GetEntry(gomock.Any(), "ev1", "e1").Return(entity.Entry{ID: "e1", VisitorID: "v1", AccountID: "acc-1", Seq: 7}, true, nil)
	m.booking.EXPECT().ReserveBatch(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, req outbound.AllocationBatchRequest) (outbound.AllocationBatchResponse, error) {
			require.Len(t, req.Entries, 1)
			e := req.Entries[0]
			assert.Equal(t, 3, e.Units, "quantity guarantee per admission")
			assert.Equal(t, "acc-1", e.AccountID)
			return outbound.AllocationBatchResponse{Allocations: []outbound.AllocationResult{{QueueEntryID: "e1", Status: "BUYER_QUOTA_EXHAUSTED"}}}, nil
		})
	// no EndEntry (strict mock would fail), no Promote
	p := worker.NewPromoter(m.store, m.settings, m.booking, m.pub, m.audit, wnow, nil, worker.PromoterTuning{BatchMax: 200})
	require.NoError(t, p.Run(ctx))
}

// Rev 6 (ADR-029): a GRANTED that follows a NO_SUPPLY_NOW in the same batch is adopted, never released. Releasing it
// would turn a live WAITING entry's Booking row into RELEASED, after which Booking answers ALREADY_ACTIVE forever and
// the Queue itself would end the buyer (the defect that motivated this change).
func TestPromoter_AdoptsAGrantThatFollowsNoSupplyAndNeverReleasesIt(t *testing.T) {
	m := newWorkerMocks(t)
	ctx := context.Background()
	m.settings.EXPECT().ListEventIDs(gomock.Any()).Return([]string{"ev1"}, nil)
	expectLease(m, ctx, "ev1")
	m.settings.EXPECT().Get(gomock.Any(), "ev1").Return(entity.EventSettings{ActiveSlotCapacity: 10}, true, nil)
	m.store.EXPECT().ActiveCount(gomock.Any(), "ev1").Return(0, nil)
	m.store.EXPECT().TopWaiting(gomock.Any(), "ev1", 10).Return([]string{"e1", "e2", "e3"}, nil)
	for _, id := range []string{"e1", "e2", "e3"} {
		m.store.EXPECT().GetEntry(gomock.Any(), "ev1", id).Return(entity.Entry{ID: id, VisitorID: "v-" + id}, true, nil)
	}
	m.booking.EXPECT().ReserveBatch(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, req outbound.AllocationBatchRequest) (outbound.AllocationBatchResponse, error) {
			for _, e := range req.Entries {
				// Adoption stays correct with 3 units per entry (ADR-030): a partial grant is honoured, an adopted row
				// is never released (asserted below), and the head still holds what it was reserved.
				assert.Equal(t, 3, e.Units, "quantity guarantee per admission")
			}
			return outbound.AllocationBatchResponse{Allocations: []outbound.AllocationResult{
				{QueueEntryID: "e1", Status: "GRANTED", AllocationID: "a1", ClaimDeadline: "2026-10-20T10:01:00Z"},
				{QueueEntryID: "e2", Status: "NO_SUPPLY_NOW"},
				{QueueEntryID: "e3", AllocationID: "a3", Status: "GRANTED", ClaimDeadline: "2026-10-20T10:01:00Z"}, // adopted
			}}, nil
		})
	// No ReleaseAllocation expectation: gomock fails the test on any release.
	m.store.EXPECT().Promote(gomock.Any(), "ev1", gomock.Any(), 10, gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ string, c []outbound.PromoteCandidate, _ int, _ int64, opts outbound.PromoteOpts) (outbound.PromoteResult, error) {
			require.Len(t, c, 2, "e1 dan e3 dipromosikan; e3 diadopsi")
			assert.Equal(t, "e1", c[0].EntryID)
			assert.Equal(t, "e3", c[1].EntryID)
			assert.Equal(t, "1", opts.LeaseToken, "Promote difagar token lease")
			return outbound.PromoteResult{Promoted: []string{"e1", "e3"}}, nil
		})
	m.pub.EXPECT().PublishTurnArrived(gomock.Any(), "ev1", "e1", "v-e1", gomock.Any()).Return(nil)
	m.pub.EXPECT().PublishTurnArrived(gomock.Any(), "ev1", "e3", "v-e3", gomock.Any()).Return(nil)

	p := worker.NewPromoter(m.store, m.settings, m.booking, m.pub, m.audit, wnow, nil, worker.PromoterTuning{BatchMax: 200})
	require.NoError(t, p.Run(ctx))
}

func TestPromoter_NothingGrantedPromotesNothing(t *testing.T) {
	m := newWorkerMocks(t)
	ctx := context.Background()
	m.settings.EXPECT().ListEventIDs(gomock.Any()).Return([]string{"ev1"}, nil)
	expectLease(m, ctx, "ev1")
	m.settings.EXPECT().Get(gomock.Any(), "ev1").Return(entity.EventSettings{ActiveSlotCapacity: 10}, true, nil)
	m.store.EXPECT().ActiveCount(gomock.Any(), "ev1").Return(0, nil)
	m.store.EXPECT().TopWaiting(gomock.Any(), "ev1", 10).Return([]string{"e1"}, nil)
	m.store.EXPECT().GetEntry(gomock.Any(), "ev1", "e1").Return(entity.Entry{ID: "e1", VisitorID: "v1"}, true, nil)
	m.booking.EXPECT().ReserveBatch(gomock.Any(), gomock.Any()).Return(outbound.AllocationBatchResponse{
		Allocations: []outbound.AllocationResult{{QueueEntryID: "e1", Status: "NO_SUPPLY_NOW"}},
	}, nil)
	// Promote must NOT be called.

	p := worker.NewPromoter(m.store, m.settings, m.booking, m.pub, m.audit, wnow, nil, worker.PromoterTuning{BatchMax: 200})
	require.NoError(t, p.Run(ctx))
}

func TestPromoter_AlreadyActiveEndsEntry(t *testing.T) {
	m := newWorkerMocks(t)
	ctx := context.Background()
	m.settings.EXPECT().ListEventIDs(gomock.Any()).Return([]string{"ev1"}, nil)
	expectLease(m, ctx, "ev1")
	m.settings.EXPECT().Get(gomock.Any(), "ev1").Return(entity.EventSettings{ActiveSlotCapacity: 10}, true, nil)
	m.store.EXPECT().ActiveCount(gomock.Any(), "ev1").Return(0, nil)
	m.store.EXPECT().TopWaiting(gomock.Any(), "ev1", 10).Return([]string{"e1"}, nil)
	m.store.EXPECT().GetEntry(gomock.Any(), "ev1", "e1").Return(entity.Entry{ID: "e1", VisitorID: "v1"}, true, nil)
	m.booking.EXPECT().ReserveBatch(gomock.Any(), gomock.Any()).Return(outbound.AllocationBatchResponse{
		Allocations: []outbound.AllocationResult{{QueueEntryID: "e1", Status: "ALREADY_ACTIVE"}},
	}, nil)
	// EndEntry called with ALREADY_ACTIVE outcome (spec 11.R1, OQ-18).
	m.store.EXPECT().EndEntry(gomock.Any(), "ev1", "e1", "ALREADY_ACTIVE", gomock.Any()).Return(outbound.EndResult{Tag: "OK", Status: "ENDED"}, nil)
	// Promote must NOT be called — no GRANTED entries.

	p := worker.NewPromoter(m.store, m.settings, m.booking, m.pub, m.audit, wnow, nil, worker.PromoterTuning{BatchMax: 200})
	require.NoError(t, p.Run(ctx))
}

func TestSweeper_MarksMissedOnly(t *testing.T) {
	m := newWorkerMocks(t)
	ctx := context.Background()
	m.settings.EXPECT().ListEventIDs(gomock.Any()).Return([]string{"ev1"}, nil)
	m.store.EXPECT().ExpiredActive(gomock.Any(), "ev1", gomock.Any(), 500).Return([]string{"e1"}, nil)
	m.store.EXPECT().EndEntry(gomock.Any(), "ev1", "e1", outbound.EndSweepMissed, gomock.Any()).Return(outbound.EndResult{Tag: "OK", Status: "MISSED"}, nil)
	m.store.EXPECT().GetSale(gomock.Any(), "ev1").Return(outbound.SaleState{SaleState: "OPEN"}, nil)
	// pub must NOT be called — Booking's own sweeper publishes booking.journey.ended (spec 11.R2, K7).

	s := worker.NewSweeper(m.store, m.settings, wnow, 500)
	require.NoError(t, s.Run(ctx))
}

func TestSweeper_AlreadyMissedNoPublish(t *testing.T) {
	m := newWorkerMocks(t)
	ctx := context.Background()
	m.settings.EXPECT().ListEventIDs(gomock.Any()).Return([]string{"ev1"}, nil)
	m.store.EXPECT().ExpiredActive(gomock.Any(), "ev1", gomock.Any(), 500).Return([]string{"e1"}, nil)
	m.store.EXPECT().EndEntry(gomock.Any(), "ev1", "e1", outbound.EndSweepMissed, gomock.Any()).Return(outbound.EndResult{Tag: "IGNORED", Status: "CLAIMED"}, nil) // not expired anymore
	m.store.EXPECT().GetSale(gomock.Any(), "ev1").Return(outbound.SaleState{SaleState: "OPEN"}, nil)

	s := worker.NewSweeper(m.store, m.settings, wnow, 500)
	require.NoError(t, s.Run(ctx))
}

// D-C8.4: the sweeper drains a sold-out event's WAITING/STALE entries, one batch after another, until a short batch
// comes back. Three full batches and a short one end in a single tick.
func TestSweeper_DrainsSoldOutWaiting(t *testing.T) {
	m := newWorkerMocks(t)
	ctx := context.Background()
	m.settings.EXPECT().ListEventIDs(gomock.Any()).Return([]string{"ev1"}, nil)
	m.store.EXPECT().ExpiredActive(gomock.Any(), "ev1", gomock.Any(), 500).Return(nil, nil)
	m.store.EXPECT().GetSale(gomock.Any(), "ev1").Return(outbound.SaleState{SaleState: "CLOSED", SoldOut: true}, nil).Times(4) // re-read before each batch
	gomock.InOrder(
		m.store.EXPECT().EndWaitingSoldOut(gomock.Any(), "ev1", gomock.Any(), outbound.SoldOutBatchSize).
			Return(outbound.SoldOutBatch{Scanned: outbound.SoldOutBatchSize, Ended: outbound.SoldOutBatchSize}, nil).Times(3),
		m.store.EXPECT().EndWaitingSoldOut(gomock.Any(), "ev1", gomock.Any(), outbound.SoldOutBatchSize).
			Return(outbound.SoldOutBatch{Scanned: 7, Ended: 7}, nil),
	)

	s := worker.NewSweeper(m.store, m.settings, wnow, 500)
	require.NoError(t, s.Run(ctx))
}

// The per-tick budget (SoldOutMaxBatches batches of SoldOutBatchSize, 100k entries) holds even when every batch is full;
// the rest of the set waits for the next tick.
func TestSweeper_DrainsSoldOutWithinTickBudget(t *testing.T) {
	m := newWorkerMocks(t)
	ctx := context.Background()
	m.settings.EXPECT().ListEventIDs(gomock.Any()).Return([]string{"ev1"}, nil)
	m.store.EXPECT().ExpiredActive(gomock.Any(), "ev1", gomock.Any(), 500).Return(nil, nil)
	m.store.EXPECT().GetSale(gomock.Any(), "ev1").Return(outbound.SaleState{SaleState: "CLOSED", SoldOut: true}, nil).Times(worker.SoldOutMaxBatches)
	m.store.EXPECT().EndWaitingSoldOut(gomock.Any(), "ev1", gomock.Any(), outbound.SoldOutBatchSize).
		Return(outbound.SoldOutBatch{Scanned: outbound.SoldOutBatchSize, Ended: outbound.SoldOutBatchSize}, nil).
		Times(worker.SoldOutMaxBatches)

	s := worker.NewSweeper(m.store, m.settings, wnow, 500)
	require.NoError(t, s.Run(ctx))
	assert.Equal(t, 200, worker.SoldOutMaxBatches)
	assert.Equal(t, 100_000, worker.SoldOutMaxBatches*outbound.SoldOutBatchSize)
}

// A sale that is not sold out (never closed, or reopened by Booking's stock return, D-C5.3) is never drained.
func TestSweeper_SoldOutFalseDoesNotDrain(t *testing.T) {
	for _, sale := range []outbound.SaleState{
		{SaleState: "OPEN"},
		{SaleState: "CLOSED"}, // manual close without soldOut
	} {
		m := newWorkerMocks(t)
		ctx := context.Background()
		m.settings.EXPECT().ListEventIDs(gomock.Any()).Return([]string{"ev1"}, nil)
		m.store.EXPECT().ExpiredActive(gomock.Any(), "ev1", gomock.Any(), 500).Return(nil, nil)
		m.store.EXPECT().GetSale(gomock.Any(), "ev1").Return(sale, nil)
		// EndWaitingSoldOut must NOT be called (a strict mock fails on an unexpected call).

		s := worker.NewSweeper(m.store, m.settings, wnow, 500)
		require.NoError(t, s.Run(ctx), sale.SaleState)
	}
}

// D-C5.3 / R28: the sale is re-read before every batch. When it reopens after the first batch, the drain stops at once:
// no second EndWaitingSoldOut, so the WAITING entries behind the first batch are never ended with SOLD_OUT.
func TestSweeper_StopsDrainWhenSaleReopens(t *testing.T) {
	m := newWorkerMocks(t)
	ctx := context.Background()
	m.settings.EXPECT().ListEventIDs(gomock.Any()).Return([]string{"ev1"}, nil)
	m.store.EXPECT().ExpiredActive(gomock.Any(), "ev1", gomock.Any(), 500).Return(nil, nil)
	gomock.InOrder(
		m.store.EXPECT().GetSale(gomock.Any(), "ev1").Return(outbound.SaleState{SaleState: "CLOSED", SoldOut: true}, nil),
		m.store.EXPECT().EndWaitingSoldOut(gomock.Any(), "ev1", gomock.Any(), outbound.SoldOutBatchSize).
			Return(outbound.SoldOutBatch{Scanned: outbound.SoldOutBatchSize, Ended: outbound.SoldOutBatchSize}, nil),
		m.store.EXPECT().GetSale(gomock.Any(), "ev1").Return(outbound.SaleState{SaleState: "OPEN", SoldOut: false}, nil),
	)
	// No further EndWaitingSoldOut: a strict mock fails on it.

	s := worker.NewSweeper(m.store, m.settings, wnow, 500)
	require.NoError(t, s.Run(ctx))
}

// A failing drain batch is returned (the tick reports it) and does not retry inside the tick.
func TestSweeper_SoldOutDrainErrorIsReturned(t *testing.T) {
	m := newWorkerMocks(t)
	ctx := context.Background()
	m.settings.EXPECT().ListEventIDs(gomock.Any()).Return([]string{"ev1"}, nil)
	m.store.EXPECT().ExpiredActive(gomock.Any(), "ev1", gomock.Any(), 500).Return(nil, nil)
	m.store.EXPECT().GetSale(gomock.Any(), "ev1").Return(outbound.SaleState{SaleState: "CLOSED", SoldOut: true}, nil)
	m.store.EXPECT().EndWaitingSoldOut(gomock.Any(), "ev1", gomock.Any(), outbound.SoldOutBatchSize).
		Return(outbound.SoldOutBatch{}, errors.New("valkey down")).Times(1)

	s := worker.NewSweeper(m.store, m.settings, wnow, 500)
	require.Error(t, s.Run(ctx))
}

func TestStatusPublisher_WritesDocument(t *testing.T) {
	m := newWorkerMocks(t)
	ctx := context.Background()
	m.settings.EXPECT().ListEventIDs(gomock.Any()).Return([]string{"ev1"}, nil)
	m.settings.EXPECT().Get(gomock.Any(), "ev1").Return(entity.EventSettings{
		ActiveSlotCapacity: 100, NearFrontMultiplier: 2, PollFarMs: 30000, PollMidMs: 10000, PollNearMs: 3000,
		CategoryLimitedThresholdPct: 5,
	}, true, nil)
	m.store.EXPECT().GetSale(gomock.Any(), "ev1").Return(outbound.SaleState{SaleState: "OPEN"}, nil)
	m.booking.EXPECT().Supply(gomock.Any(), "ev1").Return(outbound.SupplySnapshot{RemainingTickets: 50, FreeUnits: 50, MaxTicketsPerOrder: 3, Categories: []outbound.SupplyCategory{
		{Code: "VIP", Name: "VIP", Sold: 98, Total: 100},
	}}, nil)
	m.store.EXPECT().WaitingCount(gomock.Any(), "ev1").Return(int64(0), nil).AnyTimes()

	var written []byte
	m.store.EXPECT().SetStatusDoc(gomock.Any(), "ev1", gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ string, doc []byte, _ time.Duration) error { written = doc; return nil })

	sp := worker.NewStatusPublisher(m.store, m.settings, m.booking, wnow, time.Second)
	require.NoError(t, sp.Run(ctx))

	var doc dto.StatusDocument
	require.NoError(t, json.Unmarshal(written, &doc))
	assert.Equal(t, "OPEN", doc.SaleState)
	require.NotNil(t, doc.GuaranteedUntilSeq, "zones are seq-based (ADR-030 Amendment: strict FIFO); empty queue -> 0")
	assert.EqualValues(t, 0, *doc.GuaranteedUntilSeq)
	assert.Equal(t, 50, doc.RemainingTickets)
	require.Len(t, doc.Categories, 1)
	assert.Equal(t, "LIMITED", doc.Categories[0].State) // (100-98)*100=200 <= 100*5=500
}

func TestStatusPublisher_SupplyErrorStillPublishes(t *testing.T) {
	m := newWorkerMocks(t)
	ctx := context.Background()
	m.settings.EXPECT().ListEventIDs(gomock.Any()).Return([]string{"ev1"}, nil)
	m.settings.EXPECT().Get(gomock.Any(), "ev1").Return(entity.EventSettings{ActiveSlotCapacity: 100, NearFrontMultiplier: 2, PollNearMs: 3000}, true, nil)
	m.store.EXPECT().GetSale(gomock.Any(), "ev1").Return(outbound.SaleState{SaleState: "OPEN", SoldOut: true}, nil)
	m.booking.EXPECT().Supply(gomock.Any(), "ev1").Return(outbound.SupplySnapshot{}, assert.AnError)
	m.store.EXPECT().WaitingCount(gomock.Any(), "ev1").Return(int64(0), nil).AnyTimes()
	m.store.EXPECT().GetStatusDoc(gomock.Any(), "ev1").Return(nil, false, nil).AnyTimes()
	m.store.EXPECT().SetStatusDoc(gomock.Any(), "ev1", gomock.Any(), gomock.Any()).Return(nil)

	sp := worker.NewStatusPublisher(m.store, m.settings, m.booking, wnow, time.Second)
	require.NoError(t, sp.Run(ctx))
}

func TestPromoter_PublishesTurnSoon_WhenNearFront(t *testing.T) {
	m := newWorkerMocks(t)
	ctx := context.Background()

	evID := "evt-1"
	entryID := "near-entry-1"
	visitorID := "visitor-near"

	// active_slot_capacity=5, NearFrontMultiplier=2 → notify position = 10
	s := entity.EventSettings{
		EventID: evID, ActiveSlotCapacity: 5, NearFrontMultiplier: 2,
		PreTurnNotifyEnabled: true,
	}
	m.settings.EXPECT().ListEventIDs(gomock.Any()).Return([]string{evID}, nil)
	expectLease(m, ctx, evID)
	m.settings.EXPECT().Get(gomock.Any(), evID).Return(s, true, nil)
	// Slots full — no promotion path
	m.store.EXPECT().ActiveCount(gomock.Any(), evID).Return(5, nil)
	// Pre-turn notify (after the lease release): one waiting entry, seq 1, inside the position 10 window.
	m.store.EXPECT().WaitingCount(gomock.Any(), evID).Return(int64(1), nil)
	m.store.EXPECT().WaitingSeqAt(gomock.Any(), evID, int64(0)).Return(int64(1), true, nil)
	m.store.EXPECT().AdvanceNotifyWatermark(gomock.Any(), evID, int64(1)).Return(int64(0), nil)
	m.store.EXPECT().WaitingBetween(gomock.Any(), evID, int64(0), int64(1), 10).Return([]string{entryID}, nil)
	m.store.EXPECT().GetEntry(gomock.Any(), evID, entryID).Return(
		entity.Entry{ID: entryID, VisitorID: visitorID}, true, nil,
	)
	m.pub.EXPECT().PublishTurnSoon(gomock.Any(), evID, entryID, visitorID).Return(nil)

	p := worker.NewPromoter(m.store, m.settings, m.booking, m.pub, m.audit, wnow, nil, worker.PromoterTuning{BatchMax: 200})
	require.NoError(t, p.Run(ctx))
}

func TestPromoter_SkipsTurnSoon_WhenAlreadyNotified(t *testing.T) {
	m := newWorkerMocks(t)
	ctx := context.Background()

	evID := "evt-1"

	s := entity.EventSettings{
		EventID: evID, ActiveSlotCapacity: 5, NearFrontMultiplier: 2,
		PreTurnNotifyEnabled: true,
	}
	m.settings.EXPECT().ListEventIDs(gomock.Any()).Return([]string{evID}, nil)
	expectLease(m, ctx, evID)
	m.settings.EXPECT().Get(gomock.Any(), evID).Return(s, true, nil)
	m.store.EXPECT().ActiveCount(gomock.Any(), evID).Return(5, nil)
	// The watermark already covers the window's last seq (1): nothing is read or published (C8.3).
	m.store.EXPECT().WaitingCount(gomock.Any(), evID).Return(int64(1), nil)
	m.store.EXPECT().WaitingSeqAt(gomock.Any(), evID, int64(0)).Return(int64(1), true, nil)
	m.store.EXPECT().AdvanceNotifyWatermark(gomock.Any(), evID, int64(1)).Return(int64(1), nil)
	// WaitingBetween, GetEntry and PublishTurnSoon must NOT be called — already notified

	p := worker.NewPromoter(m.store, m.settings, m.booking, m.pub, m.audit, wnow, nil, worker.PromoterTuning{BatchMax: 200})
	require.NoError(t, p.Run(ctx))
}

func TestPromoter_SkipsTurnSoon_WhenDisabled(t *testing.T) {
	m := newWorkerMocks(t)
	ctx := context.Background()

	evID := "evt-1"
	s := entity.EventSettings{
		EventID: evID, ActiveSlotCapacity: 5, NearFrontMultiplier: 2,
		PreTurnNotifyEnabled: false, // disabled
	}
	m.settings.EXPECT().ListEventIDs(gomock.Any()).Return([]string{evID}, nil)
	expectLease(m, ctx, evID)
	m.settings.EXPECT().Get(gomock.Any(), evID).Return(s, true, nil)
	m.store.EXPECT().ActiveCount(gomock.Any(), evID).Return(5, nil)
	// TopWaiting must NOT be called when PreTurnNotifyEnabled=false

	p := worker.NewPromoter(m.store, m.settings, m.booking, m.pub, m.audit, wnow, nil, worker.PromoterTuning{BatchMax: 200})
	require.NoError(t, p.Run(ctx))
}

// recordingTurnSoonPub records each queue.turn.soon publish into the fake store's ordered log (C8.3).
type recordingTurnSoonPub struct {
	*fakePub
	store *fakeStore
}

func (p *recordingTurnSoonPub) PublishTurnSoon(_ context.Context, _, entryID, _ string) error {
	p.store.record("publish:" + entryID)
	return nil
}

// TC-C8.3 (PF-P1-5, D-C8.3): pre-turn notify keeps a per-event seq watermark, publishes only the seqs above it, and runs
// after the promoter lease is released (never inside the lease).
func TestPromoter_PreTurnNotifiesOnlyNewSeqsOutsideLease(t *testing.T) {
	h := newHarness(t, 5, 0, worker.PromoterTuning{})
	for i := 1; i <= 15; i++ {
		h.store.addWaiting(fmt.Sprintf("e%d", i), 0) // seq i
	}
	// Five active slots: the event is full, so the tick promotes nothing and only the pre-turn step has work.
	for i := 1; i <= 5; i++ {
		h.store.active[fmt.Sprintf("a%d", i)] = true
	}
	h.settings.s = entity.EventSettings{EventID: "ev", ActiveSlotCapacity: 5, NearFrontMultiplier: 2, PreTurnNotifyEnabled: true}
	pub := &recordingTurnSoonPub{fakePub: &fakePub{}, store: h.store}
	p := worker.NewPromoter(h.store, h.settings, h.stub, pub, fakeAudit{}, h.clk, nil, worker.PromoterTuning{BatchMax: 200})
	ctx := context.Background()

	// Tick 1: notify position 10 -> seqs 1..10 publish, after the release of this tick's lease.
	require.NoError(t, p.Run(ctx))
	want := []string{"release"}
	for i := 1; i <= 10; i++ {
		want = append(want, fmt.Sprintf("publish:e%d", i))
	}
	assert.Equal(t, want, h.store.order, "tick 1 publishes e1..e10 after the release")

	// Tick 2: the same window again publishes nothing; only the lease release is logged.
	require.NoError(t, p.Run(ctx))
	want = append(want, "release")
	assert.Equal(t, want, h.store.order, "tick 2 publishes nothing new")

	// Tick 3: the window widens to 15 -> only seqs 11..15 publish.
	h.settings.s.NearFrontMultiplier = 3
	require.NoError(t, p.Run(ctx))
	want = append(want, "release")
	for i := 11; i <= 15; i++ {
		want = append(want, fmt.Sprintf("publish:e%d", i))
	}
	assert.Equal(t, want, h.store.order, "tick 3 publishes only e11..e15, after the release")
}

// QSH-T14/T15: fault isolation. A failing entry/event must not stop the rest; errors are aggregated.

func TestSweeper_OneEntrysEndEntryErrorDoesNotStopTheOthers(t *testing.T) {
	m := newWorkerMocks(t)
	ctx := context.Background()
	m.settings.EXPECT().ListEventIDs(gomock.Any()).Return([]string{"ev1"}, nil)
	m.store.EXPECT().ExpiredActive(gomock.Any(), "ev1", gomock.Any(), 500).Return([]string{"e1", "e2"}, nil)
	m.store.EXPECT().EndEntry(gomock.Any(), "ev1", "e1", outbound.EndSweepMissed, gomock.Any()).Return(outbound.EndResult{}, errors.New("valkey down"))
	m.store.EXPECT().EndEntry(gomock.Any(), "ev1", "e2", outbound.EndSweepMissed, gomock.Any()).Return(outbound.EndResult{Tag: "OK", Status: "MISSED"}, nil)
	m.store.EXPECT().GetSale(gomock.Any(), "ev1").Return(outbound.SaleState{SaleState: "OPEN"}, nil)

	s := worker.NewSweeper(m.store, m.settings, wnow, 500)
	require.Error(t, s.Run(ctx), "galat dikumpulkan dan dikembalikan, bukan diabaikan")
}

func TestSweeper_OneEventsFailureDoesNotStopTheOthers(t *testing.T) {
	m := newWorkerMocks(t)
	ctx := context.Background()
	m.settings.EXPECT().ListEventIDs(gomock.Any()).Return([]string{"ev1", "ev2"}, nil)
	m.store.EXPECT().ExpiredActive(gomock.Any(), "ev1", gomock.Any(), 500).Return(nil, errors.New("ev1 down"))
	m.store.EXPECT().ExpiredActive(gomock.Any(), "ev2", gomock.Any(), 500).Return([]string{"e1"}, nil)
	m.store.EXPECT().EndEntry(gomock.Any(), "ev2", "e1", outbound.EndSweepMissed, gomock.Any()).Return(outbound.EndResult{Tag: "OK"}, nil)
	m.store.EXPECT().GetSale(gomock.Any(), "ev2").Return(outbound.SaleState{SaleState: "OPEN"}, nil)

	s := worker.NewSweeper(m.store, m.settings, wnow, 500)
	require.Error(t, s.Run(ctx))
}

func TestStatusPublisher_OneEventsFailureDoesNotStopTheOthers(t *testing.T) {
	m := newWorkerMocks(t)
	ctx := context.Background()
	m.settings.EXPECT().ListEventIDs(gomock.Any()).Return([]string{"ev1", "ev2"}, nil)
	m.settings.EXPECT().Get(gomock.Any(), "ev1").Return(entity.EventSettings{}, false, errors.New("pg down"))
	m.settings.EXPECT().Get(gomock.Any(), "ev2").Return(entity.EventSettings{ActiveSlotCapacity: 10}, true, nil)
	m.store.EXPECT().GetSale(gomock.Any(), "ev2").Return(outbound.SaleState{}, nil)
	m.booking.EXPECT().Supply(gomock.Any(), "ev2").Return(outbound.SupplySnapshot{}, nil)
	m.store.EXPECT().WaitingCount(gomock.Any(), "ev2").Return(int64(0), nil).AnyTimes()
	m.store.EXPECT().SetStatusDoc(gomock.Any(), "ev2", gomock.Any(), gomock.Any()).Return(nil)

	sp := worker.NewStatusPublisher(m.store, m.settings, m.booking, wnow, time.Second)
	require.Error(t, sp.Run(ctx))
}
