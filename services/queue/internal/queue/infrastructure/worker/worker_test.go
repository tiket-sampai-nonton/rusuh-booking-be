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

func TestPromoter_PromotesReservedAndPublishes(t *testing.T) {
	m := newWorkerMocks(t)
	ctx := context.Background()

	m.settings.EXPECT().ListEventIDs(ctx).Return([]string{"ev1"}, nil)
	m.settings.EXPECT().Get(ctx, "ev1").Return(entity.EventSettings{ActiveSlotCapacity: 10}, true, nil)
	m.store.EXPECT().ActiveCount(ctx, "ev1").Return(0, nil)
	m.store.EXPECT().TopWaiting(ctx, "ev1", 10).Return([]string{"e1"}, nil)
	m.store.EXPECT().GetEntry(ctx, "ev1", "e1").Return(entity.Entry{ID: "e1", VisitorID: "v1", BuyerNikHash: []byte{0xaa}}, true, nil)
	m.booking.EXPECT().ReserveBatch(ctx, gomock.Any()).Return(outbound.AllocationBatchResponse{
		Allocations: []outbound.AllocationResult{{QueueEntryID: "e1", AllocationID: "a1", Status: "GRANTED", ClaimDeadline: "2026-10-20T10:01:00Z"}},
	}, nil)
	m.store.EXPECT().Promote(ctx, "ev1", gomock.Any(), 10, gomock.Any()).Return([]string{"e1"}, nil)
	m.pub.EXPECT().PublishTurnArrived(ctx, "ev1", "e1", "v1", gomock.Any()).Return(nil)

	p := worker.NewPromoter(m.store, m.settings, m.booking, m.pub, m.audit, wnow, 200)
	require.NoError(t, p.Run(ctx))
}

func TestPromoter_NoFreeSlotsSkips(t *testing.T) {
	m := newWorkerMocks(t)
	ctx := context.Background()
	m.settings.EXPECT().ListEventIDs(ctx).Return([]string{"ev1"}, nil)
	m.settings.EXPECT().Get(ctx, "ev1").Return(entity.EventSettings{ActiveSlotCapacity: 5}, true, nil)
	m.store.EXPECT().ActiveCount(ctx, "ev1").Return(5, nil)

	p := worker.NewPromoter(m.store, m.settings, m.booking, m.pub, m.audit, wnow, 200)
	require.NoError(t, p.Run(ctx))
}

func TestPromoter_QuotaExhaustedAddsSet(t *testing.T) {
	m := newWorkerMocks(t)
	ctx := context.Background()
	m.settings.EXPECT().ListEventIDs(ctx).Return([]string{"ev1"}, nil)
	m.settings.EXPECT().Get(ctx, "ev1").Return(entity.EventSettings{ActiveSlotCapacity: 10}, true, nil)
	m.store.EXPECT().ActiveCount(ctx, "ev1").Return(0, nil)
	m.store.EXPECT().TopWaiting(ctx, "ev1", 10).Return([]string{"e1"}, nil)
	m.store.EXPECT().GetEntry(ctx, "ev1", "e1").Return(entity.Entry{ID: "e1", VisitorID: "v1", BuyerNikHash: []byte{0xaa}}, true, nil)
	m.booking.EXPECT().ReserveBatch(ctx, gomock.Any()).Return(outbound.AllocationBatchResponse{
		Allocations: []outbound.AllocationResult{{QueueEntryID: "e1", Status: "QUOTA_EXHAUSTED"}},
	}, nil)
	m.store.EXPECT().GetEntry(ctx, "ev1", "e1").Return(entity.Entry{ID: "e1", BuyerNikHash: []byte{0xaa}}, true, nil)
	m.store.EXPECT().AddQuotaExhausted(ctx, "ev1", "aa").Return(nil)
	// no GRANTED → no Promote

	p := worker.NewPromoter(m.store, m.settings, m.booking, m.pub, m.audit, wnow, 200)
	require.NoError(t, p.Run(ctx))
}

func TestSweeper_MarksMissedAndPublishes(t *testing.T) {
	m := newWorkerMocks(t)
	ctx := context.Background()
	m.settings.EXPECT().ListEventIDs(ctx).Return([]string{"ev1"}, nil)
	m.store.EXPECT().ExpiredActive(ctx, "ev1", gomock.Any()).Return([]string{"e1"}, nil)
	m.store.EXPECT().MarkMissedAndClean(ctx, "ev1", "e1", gomock.Any()).Return(true, nil)
	m.pub.EXPECT().PublishJourneyEnded(ctx, "ev1", "e1", "UNCLAIMED").Return(nil)

	s := worker.NewSweeper(m.store, m.settings, m.pub, wnow)
	require.NoError(t, s.Run(ctx))
}

func TestSweeper_AlreadyMissedNoPublish(t *testing.T) {
	m := newWorkerMocks(t)
	ctx := context.Background()
	m.settings.EXPECT().ListEventIDs(ctx).Return([]string{"ev1"}, nil)
	m.store.EXPECT().ExpiredActive(ctx, "ev1", gomock.Any()).Return([]string{"e1"}, nil)
	m.store.EXPECT().MarkMissedAndClean(ctx, "ev1", "e1", gomock.Any()).Return(false, nil) // not ADMITTED anymore

	s := worker.NewSweeper(m.store, m.settings, m.pub, wnow)
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
