//go:build integration

package worker_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	mock_outbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/_mock/queue/port/outbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/infrastructure/worker"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// reopenAfterFirstBatch wraps the real store and reopens the sale (SetSale OPEN, as Booking's stock return does through
// the status publisher, D-C5.3) right after the first EndWaitingSoldOut batch has run.
type reopenAfterFirstBatch struct {
	outbound.IQueueStore
	ev      string
	batches int
}

func (r *reopenAfterFirstBatch) EndWaitingSoldOut(ctx context.Context, ev string, nowMs int64, batch int) (outbound.SoldOutBatch, error) {
	res, err := r.IQueueStore.EndWaitingSoldOut(ctx, ev, nowMs, batch)
	r.batches++
	if r.batches == 1 {
		if serr := r.IQueueStore.SetSale(ctx, r.ev, "OPEN", false); serr != nil {
			return res, serr
		}
	}
	return res, err
}

// D-C8.4 / D-C5.3 on real Valkey: 600 WAITING entries (one full batch plus 100). The sale reopens after the first
// batch, so the sweeper ends only those 500 and leaves the other 100 WAITING.
func TestSweeper_SoldOutDrainStopsWhenSaleReopens_Real(t *testing.T) {
	store, _ := twoStores(t)
	ctx := context.Background()
	ev := fmt.Sprintf("ev-reopen-%d", time.Now().UnixNano())
	now := time.Now().UnixMilli()
	const total = 600
	for i := 1; i <= total; i++ {
		res, err := store.Join(ctx, outbound.JoinParams{
			EventID: ev, EntryID: fmt.Sprintf("entry-%d", i), NewVisitorID: fmt.Sprintf("visitor-%d", i),
			NowMs: now, OneActivePerVisitor: true,
		})
		require.NoError(t, err)
		require.Equal(t, outbound.JoinCreated, res.Outcome)
	}
	require.NoError(t, store.SetSale(ctx, ev, "CLOSED", true))

	wrapped := &reopenAfterFirstBatch{IQueueStore: store, ev: ev}
	settings := mock_outbound.NewMockIEventSettingsRepo(gomock.NewController(t))
	settings.EXPECT().ListActiveEventIDs(gomock.Any(), gomock.Any()).Return([]string{ev}, nil)

	s := worker.NewSweeper(wrapped, settings, wnow, 500)
	require.NoError(t, s.Run(ctx))

	assert.Equal(t, 1, wrapped.batches, "the drain stops after the batch that was already running")
	left, err := store.TopWaiting(ctx, ev, total)
	require.NoError(t, err)
	assert.Len(t, left, 100, "entries behind the first batch stay WAITING once the sale reopens")
	sale, err := store.GetSale(ctx, ev)
	require.NoError(t, err)
	assert.False(t, sale.SoldOut)
}
