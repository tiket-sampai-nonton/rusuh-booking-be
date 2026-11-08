package worker_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/infrastructure/worker"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// windowStore records SetSaleWindow on top of seqStore.
type windowStore struct {
	seqStore
	calls [][2]int64
	err   error
}

func (w *windowStore) SetSaleWindow(_ context.Context, _ string, startsMs, endsMs int64) error {
	w.calls = append(w.calls, [2]int64{startsMs, endsMs})
	return w.err
}

// TC-QUE-39 (R4 4.1): the publisher caches Booking's sale window; it is best-effort and never blocks the document.
func TestStatusPublisher_CachesSaleWindow(t *testing.T) {
	starts := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	ends := starts.Add(2 * time.Hour)
	tests := []struct {
		name      string
		snap      outbound.SupplySnapshot
		supplyErr error
		storeErr  error
		want      [][2]int64
	}{
		{name: "writes supply window", snap: outbound.SupplySnapshot{SalesStartsAt: starts, SalesEndsAt: ends},
			want: [][2]int64{{starts.UnixMilli(), ends.UnixMilli()}}},
		{name: "write failure does not stop publishing", snap: outbound.SupplySnapshot{SalesStartsAt: starts, SalesEndsAt: ends},
			storeErr: errors.New("valkey down"), want: [][2]int64{{starts.UnixMilli(), ends.UnixMilli()}}},
		{name: "zero times are not written", snap: outbound.SupplySnapshot{}, want: nil},
		{name: "supply error is not written", snap: outbound.SupplySnapshot{SalesStartsAt: starts, SalesEndsAt: ends},
			supplyErr: errors.New("booking down"), want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := &windowStore{err: tt.storeErr}
			sp := worker.NewStatusPublisher(st, oneSettings{s: entity.EventSettings{ActiveSlotCapacity: 10, NearFrontMultiplier: 2}},
				supplyStub{snap: tt.snap, err: tt.supplyErr}, wnow, time.Second)
			require.NoError(t, sp.Run(context.Background()))
			assert.Equal(t, tt.want, st.calls)
			assert.NotEmpty(t, st.doc, "the status document is still published")
		})
	}
}
