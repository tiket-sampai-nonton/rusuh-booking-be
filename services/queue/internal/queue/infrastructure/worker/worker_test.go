package worker_test

import (
	"context"
	"encoding/json"
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
	pii      *mock_outbound.MockIPIIProtector
}

func newWorkerMocks(t *testing.T) workerMocks {
	t.Helper()
	ctrl := gomock.NewController(t)
	pii := mock_outbound.NewMockIPIIProtector(ctrl)
	pii.EXPECT().UnsealEmail(gomock.Any()).Return("pembeli@contoh.id", nil).AnyTimes()
	pii.EXPECT().SealBuyerEmail("pembeli@contoh.id").Return("c2VhbGVk", nil).AnyTimes()
	return workerMocks{
		pii:      pii,
		store:    mock_outbound.NewMockIQueueStore(ctrl),
		settings: mock_outbound.NewMockIEventSettingsRepo(ctrl),
		booking:  mock_outbound.NewMockIBookingClient(ctrl),
		pub:      mock_outbound.NewMockIEventPublisher(ctrl),
		audit:    mock_outbound.NewMockIEntryAuditRepo(ctrl),
	}
}

// expectLease sets up the acquire+release expectations for tests that proceed past the lease check.
func expectLease(m workerMocks, ctx context.Context, eventID string) {
	m.store.EXPECT().AcquirePromoterLease(ctx, eventID, int64(10_000)).Return(true, nil)
	m.store.EXPECT().ReleasePromoterLease(ctx, eventID).Return(nil)
}

func TestPromoter_PromotesReservedAndPublishes(t *testing.T) {
	m := newWorkerMocks(t)
	ctx := context.Background()

	m.settings.EXPECT().ListEventIDs(ctx).Return([]string{"ev1"}, nil)
	expectLease(m, ctx, "ev1")
	m.settings.EXPECT().Get(ctx, "ev1").Return(entity.EventSettings{ActiveSlotCapacity: 10}, true, nil)
	m.store.EXPECT().ActiveCount(ctx, "ev1").Return(0, nil)
	m.store.EXPECT().TopWaiting(ctx, "ev1", 10).Return([]string{"e1"}, nil)
	m.store.EXPECT().GetEntry(ctx, "ev1", "e1").Return(entity.Entry{
		ID: "e1", VisitorID: "v1", BuyerNikHash: []byte{0xaa}, BuyerNikMasked: "3174********0001", AccountID: "acc-1",
	}, true, nil)
	// Booking needs the buyer's email (e-ticket), masked NIK and account (internal-booking AllocationBatchRequest).
	m.booking.EXPECT().ReserveBatch(ctx, gomock.Any()).DoAndReturn(
		func(_ context.Context, req outbound.AllocationBatchRequest) (outbound.AllocationBatchResponse, error) {
			require.Len(t, req.Entries, 1)
			assert.Equal(t, "c2VhbGVk", req.Entries[0].BuyerEmailEnc, "Booking hanya menerima ciphertext")
			assert.Equal(t, "3174********0001", req.Entries[0].BuyerNikMasked)
			assert.Equal(t, "acc-1", req.Entries[0].AccountID)
			return outbound.AllocationBatchResponse{
				Allocations: []outbound.AllocationResult{{QueueEntryID: "e1", AllocationID: "a1", Status: "GRANTED", ClaimDeadline: "2026-10-20T10:01:00Z"}},
			}, nil
		})
	m.store.EXPECT().Promote(ctx, "ev1", gomock.Any(), 10, gomock.Any()).Return([]string{"e1"}, nil)
	m.pub.EXPECT().PublishTurnArrived(ctx, "ev1", "e1", "v1", gomock.Any()).Return(nil)

	p := worker.NewPromoter(m.store, m.settings, m.booking, m.pub, m.audit, m.pii, wnow, nil, 200)
	require.NoError(t, p.Run(ctx))
}

func TestPromoter_SkipsEventWhenLeaseHeld(t *testing.T) {
	m := newWorkerMocks(t)
	ctx := context.Background()
	m.settings.EXPECT().ListEventIDs(ctx).Return([]string{"ev1"}, nil)
	// Lease already held by another instance — return false (spec 11.R3).
	m.store.EXPECT().AcquirePromoterLease(ctx, "ev1", int64(10_000)).Return(false, nil)
	// ActiveCount, TopWaiting, ReserveBatch, Promote must NOT be called.

	p := worker.NewPromoter(m.store, m.settings, m.booking, m.pub, m.audit, m.pii, wnow, nil, 200)
	require.NoError(t, p.Run(ctx))
}

func TestPromoter_NoFreeSlotsSkips(t *testing.T) {
	m := newWorkerMocks(t)
	ctx := context.Background()
	m.settings.EXPECT().ListEventIDs(ctx).Return([]string{"ev1"}, nil)
	expectLease(m, ctx, "ev1")
	m.settings.EXPECT().Get(ctx, "ev1").Return(entity.EventSettings{ActiveSlotCapacity: 5}, true, nil)
	m.store.EXPECT().ActiveCount(ctx, "ev1").Return(5, nil)

	p := worker.NewPromoter(m.store, m.settings, m.booking, m.pub, m.audit, m.pii, wnow, nil, 200)
	require.NoError(t, p.Run(ctx))
}

func TestPromoter_QuotaExhaustedAddsSet(t *testing.T) {
	m := newWorkerMocks(t)
	ctx := context.Background()
	m.settings.EXPECT().ListEventIDs(ctx).Return([]string{"ev1"}, nil)
	expectLease(m, ctx, "ev1")
	m.settings.EXPECT().Get(ctx, "ev1").Return(entity.EventSettings{ActiveSlotCapacity: 10}, true, nil)
	m.store.EXPECT().ActiveCount(ctx, "ev1").Return(0, nil)
	m.store.EXPECT().TopWaiting(ctx, "ev1", 10).Return([]string{"e1"}, nil)
	m.store.EXPECT().GetEntry(ctx, "ev1", "e1").Return(entity.Entry{ID: "e1", VisitorID: "v1", BuyerNikHash: []byte{0xaa}}, true, nil)
	m.booking.EXPECT().ReserveBatch(ctx, gomock.Any()).Return(outbound.AllocationBatchResponse{
		Allocations: []outbound.AllocationResult{{QueueEntryID: "e1", Status: "QUOTA_EXHAUSTED"}},
	}, nil)
	m.store.EXPECT().GetEntry(ctx, "ev1", "e1").Return(entity.Entry{ID: "e1", BuyerNikHash: []byte{0xaa}}, true, nil)
	m.store.EXPECT().AddQuotaExhausted(ctx, "ev1", "aa").Return(nil)
	// Entry is ended so it doesn't permanently block the queue head (spec 11.R1).
	m.store.EXPECT().EndEntry(ctx, "ev1", "e1", "BUYER_QUOTA_EXHAUSTED", gomock.Any()).Return("BUYER_QUOTA_EXHAUSTED", nil)
	// no GRANTED → no Promote

	p := worker.NewPromoter(m.store, m.settings, m.booking, m.pub, m.audit, m.pii, wnow, nil, 200)
	require.NoError(t, p.Run(ctx))
}

// Stock lower than the queue: the entries granted before the first NO_SUPPLY_NOW are admitted (their allocation is
// already committed at Booking); discarding them would leave the tail of the stock unsold forever.
func TestPromoter_PromotesTheGrantedPrefixWhenSupplyRunsOut(t *testing.T) {
	m := newWorkerMocks(t)
	ctx := context.Background()
	m.settings.EXPECT().ListEventIDs(ctx).Return([]string{"ev1"}, nil)
	expectLease(m, ctx, "ev1")
	m.settings.EXPECT().Get(ctx, "ev1").Return(entity.EventSettings{ActiveSlotCapacity: 10}, true, nil)
	m.store.EXPECT().ActiveCount(ctx, "ev1").Return(0, nil)
	m.store.EXPECT().TopWaiting(ctx, "ev1", 10).Return([]string{"e1", "e2", "e3"}, nil)
	for _, id := range []string{"e1", "e2", "e3"} {
		m.store.EXPECT().GetEntry(ctx, "ev1", id).Return(entity.Entry{ID: id, VisitorID: "v-" + id, BuyerNikHash: []byte{0xaa}}, true, nil)
	}
	m.booking.EXPECT().ReserveBatch(ctx, gomock.Any()).Return(outbound.AllocationBatchResponse{
		Allocations: []outbound.AllocationResult{
			{QueueEntryID: "e1", AllocationID: "a1", Status: "GRANTED", ClaimDeadline: "2026-10-20T10:01:00Z"},
			{QueueEntryID: "e2", Status: "NO_SUPPLY_NOW"},
			{QueueEntryID: "e3", AllocationID: "a3", Status: "GRANTED", ClaimDeadline: "2026-10-20T10:01:00Z"}, // would jump the queue
		},
	}, nil)
	m.booking.EXPECT().ReleaseAllocation(ctx, "a3", gomock.Any()).Return(nil)
	m.store.EXPECT().Promote(ctx, "ev1", gomock.Any(), 10, gomock.Any()).DoAndReturn(
		func(_ context.Context, _ string, c []outbound.PromoteCandidate, _ int, _ int64) ([]string, error) {
			require.Len(t, c, 1)
			assert.Equal(t, "e1", c[0].EntryID)
			return []string{"e1"}, nil
		})
	m.pub.EXPECT().PublishTurnArrived(ctx, "ev1", "e1", "v-e1", gomock.Any()).Return(nil)

	p := worker.NewPromoter(m.store, m.settings, m.booking, m.pub, m.audit, m.pii, wnow, nil, 200)
	require.NoError(t, p.Run(ctx))
}

func TestPromoter_NothingGrantedPromotesNothing(t *testing.T) {
	m := newWorkerMocks(t)
	ctx := context.Background()
	m.settings.EXPECT().ListEventIDs(ctx).Return([]string{"ev1"}, nil)
	expectLease(m, ctx, "ev1")
	m.settings.EXPECT().Get(ctx, "ev1").Return(entity.EventSettings{ActiveSlotCapacity: 10}, true, nil)
	m.store.EXPECT().ActiveCount(ctx, "ev1").Return(0, nil)
	m.store.EXPECT().TopWaiting(ctx, "ev1", 10).Return([]string{"e1"}, nil)
	m.store.EXPECT().GetEntry(ctx, "ev1", "e1").Return(entity.Entry{ID: "e1", VisitorID: "v1", BuyerNikHash: []byte{0xaa}}, true, nil)
	m.booking.EXPECT().ReserveBatch(ctx, gomock.Any()).Return(outbound.AllocationBatchResponse{
		Allocations: []outbound.AllocationResult{{QueueEntryID: "e1", Status: "NO_SUPPLY_NOW"}},
	}, nil)
	// Promote must NOT be called.

	p := worker.NewPromoter(m.store, m.settings, m.booking, m.pub, m.audit, m.pii, wnow, nil, 200)
	require.NoError(t, p.Run(ctx))
}

func TestPromoter_AlreadyActiveEndsEntry(t *testing.T) {
	m := newWorkerMocks(t)
	ctx := context.Background()
	m.settings.EXPECT().ListEventIDs(ctx).Return([]string{"ev1"}, nil)
	expectLease(m, ctx, "ev1")
	m.settings.EXPECT().Get(ctx, "ev1").Return(entity.EventSettings{ActiveSlotCapacity: 10}, true, nil)
	m.store.EXPECT().ActiveCount(ctx, "ev1").Return(0, nil)
	m.store.EXPECT().TopWaiting(ctx, "ev1", 10).Return([]string{"e1"}, nil)
	m.store.EXPECT().GetEntry(ctx, "ev1", "e1").Return(entity.Entry{ID: "e1", VisitorID: "v1", BuyerNikHash: []byte{0xaa}}, true, nil)
	m.booking.EXPECT().ReserveBatch(ctx, gomock.Any()).Return(outbound.AllocationBatchResponse{
		Allocations: []outbound.AllocationResult{{QueueEntryID: "e1", Status: "ALREADY_ACTIVE"}},
	}, nil)
	// EndEntry called with ALREADY_ACTIVE outcome (spec 11.R1, OQ-18).
	m.store.EXPECT().EndEntry(ctx, "ev1", "e1", "ALREADY_ACTIVE", gomock.Any()).Return("ENDED", nil)
	// Promote must NOT be called — no GRANTED entries.

	p := worker.NewPromoter(m.store, m.settings, m.booking, m.pub, m.audit, m.pii, wnow, nil, 200)
	require.NoError(t, p.Run(ctx))
}

func TestSweeper_MarksMissedOnly(t *testing.T) {
	m := newWorkerMocks(t)
	ctx := context.Background()
	m.settings.EXPECT().ListEventIDs(ctx).Return([]string{"ev1"}, nil)
	m.store.EXPECT().ExpiredActive(ctx, "ev1", gomock.Any()).Return([]string{"e1"}, nil)
	m.store.EXPECT().MarkMissedAndClean(ctx, "ev1", "e1", gomock.Any()).Return(true, nil)
	// pub must NOT be called — Booking's own sweeper publishes booking.journey.ended (spec 11.R2, K7).

	s := worker.NewSweeper(m.store, m.settings, wnow)
	require.NoError(t, s.Run(ctx))
}

func TestSweeper_AlreadyMissedNoPublish(t *testing.T) {
	m := newWorkerMocks(t)
	ctx := context.Background()
	m.settings.EXPECT().ListEventIDs(ctx).Return([]string{"ev1"}, nil)
	m.store.EXPECT().ExpiredActive(ctx, "ev1", gomock.Any()).Return([]string{"e1"}, nil)
	m.store.EXPECT().MarkMissedAndClean(ctx, "ev1", "e1", gomock.Any()).Return(false, nil) // not ADMITTED anymore

	s := worker.NewSweeper(m.store, m.settings, wnow)
	require.NoError(t, s.Run(ctx))
}

func TestStatusPublisher_WritesDocument(t *testing.T) {
	m := newWorkerMocks(t)
	ctx := context.Background()
	m.settings.EXPECT().ListEventIDs(ctx).Return([]string{"ev1"}, nil)
	m.settings.EXPECT().Get(ctx, "ev1").Return(entity.EventSettings{
		ActiveSlotCapacity: 100, NearFrontMultiplier: 2, PollFarMs: 30000, PollMidMs: 10000, PollNearMs: 3000,
		CategoryLimitedThresholdPct: 5,
	}, true, nil)
	m.store.EXPECT().GetSale(ctx, "ev1").Return(outbound.SaleState{SaleState: "OPEN", ServingSeq: 100}, nil)
	m.booking.EXPECT().Supply(ctx, "ev1").Return(outbound.SupplySnapshot{RemainingTickets: 50, Categories: []outbound.SupplyCategory{
		{Code: "VIP", Name: "VIP", Sold: 98, Total: 100},
	}}, nil)

	var written []byte
	m.store.EXPECT().SetStatusDoc(ctx, "ev1", gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ string, doc []byte, _ time.Duration) error { written = doc; return nil })

	sp := worker.NewStatusPublisher(m.store, m.settings, m.booking, wnow, time.Second)
	require.NoError(t, sp.Run(ctx))

	var doc dto.StatusDocument
	require.NoError(t, json.Unmarshal(written, &doc))
	assert.Equal(t, "OPEN", doc.SaleState)
	assert.Equal(t, int64(100), doc.ServingSeq)
	assert.Equal(t, int64(150), doc.GuaranteedUntilSeq) // 100 + 50
	assert.Equal(t, int64(200), doc.PossibleUntilSeq)   // 100 + 50*2
	assert.Equal(t, 50, doc.RemainingTickets)
	require.Len(t, doc.Categories, 1)
	assert.Equal(t, "LIMITED", doc.Categories[0].State) // (100-98)*100=200 <= 100*5=500
}

func TestStatusPublisher_SupplyErrorStillPublishes(t *testing.T) {
	m := newWorkerMocks(t)
	ctx := context.Background()
	m.settings.EXPECT().ListEventIDs(ctx).Return([]string{"ev1"}, nil)
	m.settings.EXPECT().Get(ctx, "ev1").Return(entity.EventSettings{ActiveSlotCapacity: 100, NearFrontMultiplier: 2, PollNearMs: 3000}, true, nil)
	m.store.EXPECT().GetSale(ctx, "ev1").Return(outbound.SaleState{SaleState: "OPEN", ServingSeq: 10, SoldOut: true}, nil)
	m.booking.EXPECT().Supply(ctx, "ev1").Return(outbound.SupplySnapshot{}, assert.AnError)
	m.store.EXPECT().SetStatusDoc(ctx, "ev1", gomock.Any(), gomock.Any()).Return(nil)

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
	m.settings.EXPECT().ListEventIDs(ctx).Return([]string{evID}, nil)
	expectLease(m, ctx, evID)
	m.settings.EXPECT().Get(ctx, evID).Return(s, true, nil)
	// Slots full — no promotion path
	m.store.EXPECT().ActiveCount(ctx, evID).Return(5, nil)
	// Pre-turn notify: fetch top 10 waiting entries
	m.store.EXPECT().TopWaiting(ctx, evID, 10).Return([]string{entryID}, nil)
	m.store.EXPECT().GetEntry(ctx, evID, entryID).Return(
		entity.Entry{ID: entryID, VisitorID: visitorID}, true, nil,
	)
	m.store.EXPECT().WasNotifiedSoon(ctx, evID, entryID).Return(false, nil)
	m.pub.EXPECT().PublishTurnSoon(ctx, evID, entryID, visitorID).Return(nil)
	m.store.EXPECT().MarkNotifiedSoon(ctx, evID, entryID).Return(nil)

	p := worker.NewPromoter(m.store, m.settings, m.booking, m.pub, m.audit, m.pii, wnow, nil, 200)
	require.NoError(t, p.Run(ctx))
}

func TestPromoter_SkipsTurnSoon_WhenAlreadyNotified(t *testing.T) {
	m := newWorkerMocks(t)
	ctx := context.Background()

	evID := "evt-1"
	entryID := "near-entry-1"

	s := entity.EventSettings{
		EventID: evID, ActiveSlotCapacity: 5, NearFrontMultiplier: 2,
		PreTurnNotifyEnabled: true,
	}
	m.settings.EXPECT().ListEventIDs(ctx).Return([]string{evID}, nil)
	expectLease(m, ctx, evID)
	m.settings.EXPECT().Get(ctx, evID).Return(s, true, nil)
	m.store.EXPECT().ActiveCount(ctx, evID).Return(5, nil)
	m.store.EXPECT().TopWaiting(ctx, evID, 10).Return([]string{entryID}, nil)
	m.store.EXPECT().WasNotifiedSoon(ctx, evID, entryID).Return(true, nil)
	// GetEntry and PublishTurnSoon must NOT be called — already notified

	p := worker.NewPromoter(m.store, m.settings, m.booking, m.pub, m.audit, m.pii, wnow, nil, 200)
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
	m.settings.EXPECT().ListEventIDs(ctx).Return([]string{evID}, nil)
	expectLease(m, ctx, evID)
	m.settings.EXPECT().Get(ctx, evID).Return(s, true, nil)
	m.store.EXPECT().ActiveCount(ctx, evID).Return(5, nil)
	// TopWaiting must NOT be called when PreTurnNotifyEnabled=false

	p := worker.NewPromoter(m.store, m.settings, m.booking, m.pub, m.audit, m.pii, wnow, nil, 200)
	require.NoError(t, p.Run(ctx))
}
