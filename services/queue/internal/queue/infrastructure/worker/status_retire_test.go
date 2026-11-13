package worker_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/valueobject"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/infrastructure/worker"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// TC-C8.2 (D-C8.2, P3-13): a finished event's fixed keys get a TTL only once its sale has ended and nothing in it is
// live. ADMITTED holds a slot; WAITING and STALE can still be admitted or revived.
func TestStatusPublisher_RetiresOnlyEndedEmptyEvents(t *testing.T) {
	now := wnow.Now()
	endedSale := outbound.SupplySnapshot{SalesStartsAt: now.Add(-48 * time.Hour), SalesEndsAt: now.Add(-time.Hour)}
	openSale := outbound.SupplySnapshot{SalesStartsAt: now.Add(-48 * time.Hour), SalesEndsAt: now.Add(time.Hour)}

	admitted := func(s *fakeStore) {
		s.entries["a"] = &entity.Entry{ID: "a", Status: valueobject.StatusAdmitted}
		s.active["a"] = true
	}
	waiting := func(s *fakeStore) { s.addWaitingAt("w", 1) }
	stale := func(s *fakeStore) { s.entries["s"] = &entity.Entry{ID: "s", Status: valueobject.StatusStale} }
	ended := func(s *fakeStore) { s.entries["e"] = &entity.Entry{ID: "e", Status: valueobject.StatusEnded} }

	tests := []struct {
		name        string
		snap        outbound.SupplySnapshot
		supplyErr   error
		setup       func(*fakeStore)
		wantRetired bool
	}{
		{name: "sale still open", snap: openSale},
		{name: "sale end unknown (zero window)", snap: outbound.SupplySnapshot{}},
		{name: "booking unreadable: sale end unknown", snap: endedSale, supplyErr: errors.New("booking down")},
		{name: "admitted entry still holds a slot", snap: endedSale, setup: admitted},
		{name: "waiting entry is still live", snap: endedSale, setup: waiting},
		{name: "stale entry can still be revived", snap: endedSale, setup: stale},
		{name: "only ended entries left", snap: endedSale, setup: ended, wantRetired: true},
		{name: "ended and empty", snap: endedSale, wantRetired: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := newFakeStore()
			if tt.setup != nil {
				tt.setup(st)
			}
			sp := worker.NewStatusPublisher(st, oneSettings{s: entity.EventSettings{ActiveSlotCapacity: 10, NearFrontMultiplier: 2}},
				supplyStub{snap: tt.snap, err: tt.supplyErr}, wnow, time.Second)
			require.NoError(t, sp.Run(context.Background()))

			var want []retireCall
			if tt.wantRetired {
				want = []retireCall{{eventID: "ev", ttl: outbound.EntryRetention}}
			}
			assert.Equal(t, want, st.retired)
		})
	}
}
