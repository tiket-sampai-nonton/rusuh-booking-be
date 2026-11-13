package worker_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// FR-09 / SRS-F-05-10: admissionRatePerMin is measured from promote.lua's per-minute counters; 0 hides the client ETA
// while the rate is not a usable estimate.
func TestStatusPublisher_AdmissionRate(t *testing.T) {
	nowMs := wnow.T.UnixMilli()
	nowMin := nowMs / 60_000 // wnow is on a minute boundary: the last complete minute is nowMin-1
	opened := wnow.T.Add(-time.Hour)
	settled := outbound.SaleState{Capacity: 10, CapacitySinceMs: opened.UnixMilli()}
	supply := func(mod func(*outbound.SupplySnapshot)) outbound.SupplySnapshot {
		s := outbound.SupplySnapshot{FreeUnits: 50, MaxTicketsPerOrder: 3, RemainingTickets: 50,
			SalesStartsAt: opened, SalesEndsAt: wnow.T.Add(time.Hour)}
		if mod != nil {
			mod(&s)
		}
		return s
	}
	// ago maps "minutes before now" to a count, e.g. ago(1, 3) = 3 admissions in the last complete minute.
	ago := func(pairs ...int64) map[int64]int64 {
		m := map[int64]int64{}
		for i := 0; i < len(pairs); i += 2 {
			m[nowMin-pairs[i]] = pairs[i+1]
		}
		return m
	}
	steady := ago(1, 3, 2, 3, 3, 3, 4, 3, 5, 3)

	cases := []struct {
		name   string
		sale   outbound.SaleState
		adm    map[int64]int64
		active int
		snap   outbound.SupplySnapshot
		serr   error
		want   int
	}{
		{name: "steady: averages the minutes needed for 10 admissions", sale: settled, adm: steady, snap: supply(nil), want: 3},
		{name: "low capacity: 10 admissions over 13 minutes round to 1", sale: settled,
			adm: ago(1, 1, 2, 1, 4, 1, 5, 1, 7, 1, 8, 1, 10, 1, 11, 1, 12, 1, 13, 1), snap: supply(nil), want: 1},
		{name: "fewer than 10 admissions in 15 minutes hides it", sale: settled,
			adm: ago(1, 1, 2, 1, 3, 1, 4, 1, 5, 1, 6, 1, 7, 1, 8, 1, 9, 1, 16, 50), snap: supply(nil), want: 0},
		{name: "the opening burst minute is left out", sale: settled, adm: ago(3, 500, 2, 5, 1, 5),
			snap: supply(func(s *outbound.SupplySnapshot) { s.SalesStartsAt = wnow.T.Add(-3 * time.Minute) }), want: 5},
		{name: "a capacity change settles like an opening",
			sale: outbound.SaleState{Capacity: 10, CapacitySinceMs: nowMs - 4*60_000 + 30_000},
			adm:  ago(4, 200, 3, 200, 2, 6, 1, 6), snap: supply(nil), want: 6},
		{name: "draining after a capacity cut hides it", sale: settled, adm: steady, active: 12, snap: supply(nil), want: 0},
		{name: "no free units (only held stock) hides it", sale: settled, adm: steady,
			snap: supply(func(s *outbound.SupplySnapshot) { s.FreeUnits, s.HeldUnits = 0, 9 }), want: 0},
		{name: "sold out hides it", sale: settled, adm: steady,
			snap: supply(func(s *outbound.SupplySnapshot) { s.SoldOut = true }), want: 0},
		{name: "booking unavailable hides it", sale: settled, adm: steady, serr: errors.New("down"), want: 0},
		{name: "sale not started hides it", sale: settled, adm: steady,
			snap: supply(func(s *outbound.SupplySnapshot) { s.SalesStartsAt = wnow.T.Add(10 * time.Minute) }), want: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := &seqStore{sale: tc.sale, adm: tc.adm, active: tc.active}
			doc := publishWith(t, st, entity.EventSettings{ActiveSlotCapacity: 10, NearFrontMultiplier: 2}, tc.snap, tc.serr)
			assert.Equal(t, tc.want, doc.AdmissionRatePerMin)
		})
	}
}

// A capacity the publisher has not seen before is recorded with the current time, and the rate stays hidden until
// the minutes after it settle; an unchanged capacity is not rewritten.
func TestStatusPublisher_AdmissionRateRecordsCapacityChange(t *testing.T) {
	nowMs := wnow.T.UnixMilli()
	nowMin := nowMs / 60_000
	snap := outbound.SupplySnapshot{FreeUnits: 50, MaxTicketsPerOrder: 3, RemainingTickets: 50,
		SalesStartsAt: wnow.T.Add(-time.Hour), SalesEndsAt: wnow.T.Add(time.Hour)}
	adm := map[int64]int64{nowMin - 1: 5, nowMin - 2: 5}
	s := entity.EventSettings{ActiveSlotCapacity: 10, NearFrontMultiplier: 2}

	changed := &seqStore{sale: outbound.SaleState{Capacity: 5, CapacitySinceMs: nowMs - 3_600_000}, adm: adm}
	doc := publishWith(t, changed, s, snap, nil)
	assert.Equal(t, [][2]int64{{10, nowMs}}, changed.capSets)
	assert.Zero(t, doc.AdmissionRatePerMin)

	same := &seqStore{sale: outbound.SaleState{Capacity: 10, CapacitySinceMs: nowMs - 3_600_000}, adm: adm}
	doc = publishWith(t, same, s, snap, nil)
	assert.Empty(t, same.capSets)
	assert.Equal(t, 5, doc.AdmissionRatePerMin)
}

// A failed counter read does not stop the document: it is published with the rate hidden.
func TestStatusPublisher_AdmissionRateReadFailureStillPublishes(t *testing.T) {
	nowMs := wnow.T.UnixMilli()
	st := &seqStore{sale: outbound.SaleState{Capacity: 10, CapacitySinceMs: nowMs - 3_600_000}, admErr: errors.New("valkey down")}
	snap := outbound.SupplySnapshot{FreeUnits: 50, MaxTicketsPerOrder: 3, RemainingTickets: 50,
		SalesStartsAt: wnow.T.Add(-time.Hour), SalesEndsAt: wnow.T.Add(time.Hour)}
	doc := publishWith(t, st, entity.EventSettings{ActiveSlotCapacity: 10, NearFrontMultiplier: 2}, snap, nil)
	require.Equal(t, 1, st.docWrites)
	assert.Zero(t, doc.AdmissionRatePerMin)
}
