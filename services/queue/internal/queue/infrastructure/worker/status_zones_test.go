package worker_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/infrastructure/worker"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// seqStore is a partial IQueueStore for the publisher: the live waiting seqs in order and the status doc. The
// opening-window draw was dropped (ADR-030 Amendment); all joins are strict FIFO by seq, so there is no drawn flag.
type seqStore struct {
	outbound.IQueueStore
	seqs []int64
	doc  []byte
	prev []byte
	sale outbound.SaleState // zero value reads as OPEN (the publisher's default)
	sets []saleSet          // SetSale calls, in order
}

// saleSet records one SetSale invocation.
type saleSet struct {
	state   string
	soldOut bool
}

func (s *seqStore) GetSale(context.Context, string) (outbound.SaleState, error) {
	return s.sale, nil
}
func (s *seqStore) SetSale(_ context.Context, _ string, state string, soldOut bool) error {
	s.sets = append(s.sets, saleSet{state: state, soldOut: soldOut})
	return nil
}
func (s *seqStore) SetSaleWindow(context.Context, string, int64, int64) error { return nil }
func (s *seqStore) WaitingCount(context.Context, string) (int64, error) {
	return int64(len(s.seqs)), nil
}
func (s *seqStore) WaitingSeqAt(_ context.Context, _ string, i int64) (int64, bool, error) {
	if i < 0 || int(i) >= len(s.seqs) {
		return 0, false, nil
	}
	return s.seqs[i], true, nil
}
func (s *seqStore) GetStatusDoc(context.Context, string) ([]byte, bool, error) {
	return s.prev, s.prev != nil, nil
}
func (s *seqStore) SetStatusDoc(_ context.Context, _ string, d []byte, _ time.Duration) error {
	s.doc = d
	return nil
}

type oneSettings struct {
	outbound.IEventSettingsRepo
	s entity.EventSettings
}

func (o oneSettings) ListEventIDs(context.Context) ([]string, error) { return []string{"ev"}, nil }
func (o oneSettings) Get(context.Context, string) (entity.EventSettings, bool, error) {
	return o.s, true, nil
}

type supplyStub struct {
	outbound.IBookingClient
	snap outbound.SupplySnapshot
	err  error
}

func (b supplyStub) Supply(context.Context, string) (outbound.SupplySnapshot, error) {
	return b.snap, b.err
}

func publish(t *testing.T, st *seqStore, snap outbound.SupplySnapshot, serr error) dto.StatusDocument {
	t.Helper()
	return publishWith(t, st, entity.EventSettings{ActiveSlotCapacity: 10, NearFrontMultiplier: 2}, snap, serr)
}

func publishWith(t *testing.T, st *seqStore, s entity.EventSettings, snap outbound.SupplySnapshot, serr error) dto.StatusDocument {
	t.Helper()
	sp := worker.NewStatusPublisher(st, oneSettings{s: s}, supplyStub{snap: snap, err: serr}, wnow, time.Second)
	require.NoError(t, sp.Run(context.Background()))
	var doc dto.StatusDocument
	require.NoError(t, json.Unmarshal(st.doc, &doc))
	return doc
}

func seqRange(from, to int64) []int64 {
	var r []int64
	for i := from; i <= to; i++ {
		r = append(r, i)
	}
	return r
}

func val(p *int64) int64 {
	if p == nil {
		return -1
	}
	return *p
}

// TC-ANON-07 cases 1-5 (R05.1): zones are entry-count over order, expressed on seq (strict FIFO).
func TestStatusPublisher_ZonesOnSeq(t *testing.T) {
	sup := func(f, a, h int) outbound.SupplySnapshot {
		return outbound.SupplySnapshot{FreeUnits: f, AllocatedUnits: a, HeldUnits: h, MaxTicketsPerOrder: 3, RemainingTickets: f}
	}
	t.Run("case 1: F=7 A=3 H=2 -> G=3 P=4", func(t *testing.T) {
		doc := publish(t, &seqStore{seqs: seqRange(1, 20)}, sup(7, 3, 2), nil)
		assert.EqualValues(t, 3, val(doc.GuaranteedUntilSeq))
		assert.EqualValues(t, 4, val(doc.PossibleUntilSeq))
		assert.EqualValues(t, 1, val(doc.HeadSeq))
	})
	t.Run("case 2: seqs 1-2 STALE (not live) -> boundary is the seq of the 3rd live entry", func(t *testing.T) {
		doc := publish(t, &seqStore{seqs: seqRange(3, 20)}, sup(7, 3, 2), nil)
		assert.EqualValues(t, 5, val(doc.GuaranteedUntilSeq))
		assert.EqualValues(t, 6, val(doc.PossibleUntilSeq))
		assert.EqualValues(t, 3, val(doc.HeadSeq))
	})
	t.Run("case 3: F=1 -> G=1 P=1", func(t *testing.T) {
		doc := publish(t, &seqStore{seqs: seqRange(1, 5)}, sup(1, 0, 0), nil)
		assert.EqualValues(t, 1, val(doc.GuaranteedUntilSeq))
		assert.EqualValues(t, 1, val(doc.PossibleUntilSeq))
	})
	t.Run("case 4: F=0 A=6 -> G=0 (0 = empty zone) P=2", func(t *testing.T) {
		doc := publish(t, &seqStore{seqs: seqRange(1, 5)}, sup(0, 6, 0), nil)
		assert.EqualValues(t, 0, val(doc.GuaranteedUntilSeq))
		assert.EqualValues(t, 2, val(doc.PossibleUntilSeq))
	})
	t.Run("fewer live entries than G: last live seq", func(t *testing.T) {
		doc := publish(t, &seqStore{seqs: []int64{4, 9}}, sup(30, 0, 0), nil)
		assert.EqualValues(t, 9, val(doc.GuaranteedUntilSeq))
	})
	t.Run("empty queue: zeros", func(t *testing.T) {
		doc := publish(t, &seqStore{}, sup(30, 0, 0), nil)
		assert.EqualValues(t, 0, val(doc.GuaranteedUntilSeq))
		assert.EqualValues(t, 0, val(doc.HeadSeq))
	})
}

// Booking down: keep the previous boundaries rather than telling everyone "not enough" (protect the UX).
func TestStatusPublisher_SupplyDownKeepsPreviousBoundaries(t *testing.T) {
	g, p := int64(12), int64(20)
	prev, _ := json.Marshal(dto.StatusDocument{GuaranteedUntilSeq: &g, PossibleUntilSeq: &p})
	doc := publish(t, &seqStore{seqs: seqRange(1, 5), prev: prev}, outbound.SupplySnapshot{}, assert.AnError)
	assert.EqualValues(t, 12, val(doc.GuaranteedUntilSeq))
	assert.EqualValues(t, 20, val(doc.PossibleUntilSeq))
	assert.EqualValues(t, 1, val(doc.HeadSeq))
}

// A sold-out sale reopens only when Booking has free stock again and the sale window is still open (D-C5.3).
// Booking reports a cancelled event as soldOut, so a cancelled or ended sale must stay closed.
func TestStatusPublisher_ReopensSaleWhenStockReturns(t *testing.T) {
	now := wnow.T
	soldOutSale := outbound.SaleState{SaleState: dto.SaleClosed, SoldOut: true}
	open := outbound.SupplySnapshot{RemainingTickets: 3, FreeUnits: 3, SalesStartsAt: now.Add(-time.Hour), SalesEndsAt: now.Add(time.Hour)}
	tests := []struct {
		name        string
		sale        outbound.SaleState
		snap        outbound.SupplySnapshot
		wantSets    []saleSet
		wantSoldOut bool
	}{
		{name: "stock back inside the window reopens", sale: soldOutSale, snap: open,
			wantSets: []saleSet{{state: dto.SaleOpen, soldOut: false}}, wantSoldOut: false},
		{name: "window ended stays closed", sale: soldOutSale, snap: withWindow(open, now.Add(-time.Hour), now.Add(-time.Minute)),
			wantSets: nil, wantSoldOut: true},
		{name: "window not started stays closed", sale: soldOutSale, snap: withWindow(open, now.Add(time.Minute), now.Add(time.Hour)),
			wantSets: nil, wantSoldOut: true},
		{name: "cancelled (Booking soldOut) stays closed", sale: soldOutSale,
			snap:     outbound.SupplySnapshot{SoldOut: true, RemainingTickets: 3, SalesStartsAt: now.Add(-time.Hour), SalesEndsAt: now.Add(time.Hour)},
			wantSets: nil, wantSoldOut: true},
		{name: "no free stock stays closed", sale: soldOutSale, snap: withStock(open, 0), wantSets: nil, wantSoldOut: true},
		{name: "manual close without soldOut is not reopened", sale: outbound.SaleState{SaleState: dto.SaleClosed}, snap: open,
			wantSets: nil, wantSoldOut: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := &seqStore{sale: tt.sale}
			doc := publish(t, st, tt.snap, nil)
			assert.Equal(t, tt.wantSets, st.sets)
			assert.Equal(t, tt.wantSoldOut, doc.SoldOut)
		})
	}
}

func withWindow(s outbound.SupplySnapshot, starts, ends time.Time) outbound.SupplySnapshot {
	s.SalesStartsAt, s.SalesEndsAt = starts, ends
	return s
}

func withStock(s outbound.SupplySnapshot, remaining int) outbound.SupplySnapshot {
	s.RemainingTickets, s.FreeUnits = remaining, remaining
	return s
}

// TC-ANON-07 (AC-C5.5 P1-12): LIMITED is derived from the real sold/total of each category.
func TestCategoriesFrom_LimitedWithRealSoldTotal(t *testing.T) {
	s := entity.EventSettings{ActiveSlotCapacity: 10, NearFrontMultiplier: 2, CategoryLimitedThresholdPct: 5}
	tests := []struct {
		name        string
		sold, total int
		want        string
	}{
		{name: "4 of 100 left is LIMITED at 5%", sold: 96, total: 100, want: dto.CategoryLimited},
		{name: "10 of 100 left is AVAILABLE", sold: 90, total: 100, want: dto.CategoryAvailable},
		{name: "none left is SOLD_OUT", sold: 100, total: 100, want: dto.CategorySoldOut},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snap := outbound.SupplySnapshot{RemainingTickets: tt.total - tt.sold,
				Categories: []outbound.SupplyCategory{{Code: "VIP", Name: "VIP", Sold: tt.sold, Total: tt.total}}}
			doc := publishWith(t, &seqStore{}, s, snap, nil)
			require.Len(t, doc.Categories, 1)
			assert.Equal(t, tt.want, doc.Categories[0].State)
		})
	}
}
