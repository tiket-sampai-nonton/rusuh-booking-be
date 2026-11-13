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

// windowSettings records every UpdateSaleWindow attempt; writeErr makes the attempts fail.
type windowSettings struct {
	oneSettings
	attempts [][2]int64
	writeErr error
}

func (w *windowSettings) ListActiveEventIDs(context.Context, time.Time) ([]string, error) {
	return []string{"ev"}, nil
}

func (w *windowSettings) UpdateSaleWindow(_ context.Context, _ string, starts, ends time.Time) error {
	w.attempts = append(w.attempts, [2]int64{starts.UnixMilli(), ends.UnixMilli()})
	return w.writeErr
}

// stepSupply answers Supply from fields the test changes between ticks.
type stepSupply struct {
	outbound.IBookingClient
	snap outbound.SupplySnapshot
	err  error
}

func (b *stepSupply) Supply(context.Context, string) (outbound.SupplySnapshot, error) {
	return b.snap, b.err
}

// TC-C8.10 (D-C8.10): the status publisher writes the sale window to event_settings only when it differs from the value this
// process last wrote; a failed write is not remembered, so the next tick retries it.
func TestStatusPublisher_WritesSaleWindowOnlyOnChange(t *testing.T) {
	w1 := outbound.SupplySnapshot{SalesStartsAt: wnow.Now().Add(-24 * time.Hour), SalesEndsAt: wnow.Now().Add(24 * time.Hour)}
	w2 := outbound.SupplySnapshot{SalesStartsAt: w1.SalesStartsAt, SalesEndsAt: wnow.Now().Add(48 * time.Hour)}
	write := func(s outbound.SupplySnapshot) [2]int64 {
		return [2]int64{s.SalesStartsAt.UnixMilli(), s.SalesEndsAt.UnixMilli()}
	}
	errBoom := errors.New("postgres down")

	st := &windowSettings{oneSettings: oneSettings{s: entity.EventSettings{ActiveSlotCapacity: 10, NearFrontMultiplier: 2}}}
	supply := &stepSupply{snap: w1}
	sp := worker.NewStatusPublisher(newFakeStore(), st, supply, wnow, time.Second)
	tick := func(t *testing.T) { t.Helper(); require.NoError(t, sp.Run(context.Background())) }

	tick(t)
	assert.Equal(t, [][2]int64{write(w1)}, st.attempts, "first sight of the window is written")

	tick(t)
	assert.Len(t, st.attempts, 1, "an unchanged window is not written again")

	supply.snap = w2
	tick(t)
	assert.Equal(t, [][2]int64{write(w1), write(w2)}, st.attempts, "a changed window is written")

	supply.err = errors.New("booking down")
	tick(t)
	assert.Len(t, st.attempts, 2, "no write while Booking's window is unreadable")
	supply.err = nil

	st.writeErr = errBoom
	supply.snap = w1
	tick(t)
	assert.Len(t, st.attempts, 3, "a changed window is attempted")
	tick(t)
	assert.Len(t, st.attempts, 4, "a failed write is retried on the next tick")

	st.writeErr = nil
	tick(t)
	assert.Len(t, st.attempts, 5, "the retry succeeds")
	tick(t)
	assert.Len(t, st.attempts, 5, "after a successful write the window is quiet again")
}

// C8.10 Part B (R40, I2): a window whose end is not after its start is never written, because it would drop the event from
// the iteration for good. It is not remembered either, so the next sane window is written.
func TestStatusPublisher_RejectsWindowEndingNotAfterStart(t *testing.T) {
	at := wnow.Now()
	equal := outbound.SupplySnapshot{SalesStartsAt: at, SalesEndsAt: at}
	backwards := outbound.SupplySnapshot{SalesStartsAt: at.Add(time.Hour), SalesEndsAt: at}
	sane := outbound.SupplySnapshot{SalesStartsAt: at.Add(-time.Hour), SalesEndsAt: at.Add(time.Hour)}

	st := &windowSettings{oneSettings: oneSettings{s: entity.EventSettings{ActiveSlotCapacity: 10, NearFrontMultiplier: 2}}}
	supply := &stepSupply{snap: equal}
	sp := worker.NewStatusPublisher(newFakeStore(), st, supply, wnow, time.Second)
	tick := func(t *testing.T) { t.Helper(); require.NoError(t, sp.Run(context.Background())) }

	tick(t)
	assert.Empty(t, st.attempts, "ends equal to starts is not written")
	supply.snap = backwards
	tick(t)
	assert.Empty(t, st.attempts, "ends before starts is not written")
	supply.snap = sane
	tick(t)
	assert.Equal(t, [][2]int64{{sane.SalesStartsAt.UnixMilli(), sane.SalesEndsAt.UnixMilli()}}, st.attempts,
		"a sane window is written; the rejected ones were not remembered")
}
