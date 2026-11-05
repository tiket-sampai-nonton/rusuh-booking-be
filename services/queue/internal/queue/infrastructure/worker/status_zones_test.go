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
}

func (s *seqStore) GetSale(context.Context, string) (outbound.SaleState, error) {
	return outbound.SaleState{SaleState: "OPEN"}, nil
}
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
	sp := worker.NewStatusPublisher(st, oneSettings{s: entity.EventSettings{ActiveSlotCapacity: 10, NearFrontMultiplier: 2}},
		supplyStub{snap: snap, err: serr}, wnow, time.Second)
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
