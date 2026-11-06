package worker_test

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/infrastructure/worker"
)

// mutableSettings is a settings repo whose rows can appear, change and vanish between ticks, like the admin PUT does.
// Get is read on every call; nothing is cached by the fake, so any caching would have to be in the worker (QSA-7.1).
type mutableSettings struct {
	mu   sync.Mutex
	rows map[string]entity.EventSettings
}

func (m *mutableSettings) put(s entity.EventSettings) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows[s.EventID] = s
}

func (m *mutableSettings) Get(_ context.Context, id string) (entity.EventSettings, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.rows[id]
	return s, ok, nil
}

func (m *mutableSettings) ListEventIDs(context.Context) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]string, 0, len(m.rows))
	for id := range m.rows {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

// listedButRowless lists an id whose row is gone (deleted between ListEventIDs and Get).
type listedButRowless struct{ mutableSettings }

func (l *listedButRowless) ListEventIDs(context.Context) ([]string, error) {
	return []string{"ev"}, nil
}

func newMutableHarness(t *testing.T, st interface {
	Get(context.Context, string) (entity.EventSettings, bool, error)
	ListEventIDs(context.Context) ([]string, error)
}, stock int) *harness {
	t.Helper()
	h := &harness{t: t, store: newFakeStore(), metrics: newFakeMetrics(), pub: &fakePub{}, pii: &fakePII{}}
	h.clk = &clock.Fixed{T: time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)}
	h.stub = newBookingStub(h.clk, 3*time.Minute, stock)
	h.p = worker.NewPromoter(h.store, st, h.stub, h.pub, fakeAudit{}, h.clk,
		slog.New(slog.NewTextHandler(discard{}, nil)), worker.PromoterTuning{BatchMax: 200}).WithMetrics(h.metrics)
	return h
}

// TC-CFG-11 / QSA-7.2: a capacity change between two ticks changes the free computation of the second tick,
// without restart and without any cache.
func TestPromoter_CapacityChangeIsPickedUpNextTick(t *testing.T) {
	ms := &mutableSettings{rows: map[string]entity.EventSettings{"ev": {EventID: "ev", ActiveSlotCapacity: 1}}}
	h := newMutableHarness(t, ms, 10)
	h.store.addWaiting("a", 1)
	h.store.addWaiting("b", 2)
	h.store.addWaiting("c", 3)

	h.mustTick()
	assert.Equal(t, "ADMITTED", h.status("a"))
	assert.Equal(t, "WAITING", h.status("b"), "kapasitas 1: hanya satu yang masuk")

	ms.put(entity.EventSettings{EventID: "ev", ActiveSlotCapacity: 3}) // the admin raises capacity
	h.mustTick()
	assert.Equal(t, "ADMITTED", h.status("b"), "tick kedua memakai kapasitas baru")
	assert.Equal(t, "ADMITTED", h.status("c"))
}

// QSA-7.4: an event listed without a row is skipped quietly: no error, no Booking call, no write.
func TestPromoter_RowlessEventIsANoOp(t *testing.T) {
	ls := &listedButRowless{mutableSettings{rows: map[string]entity.EventSettings{}}}
	h := newMutableHarness(t, ls, 10)
	h.store.addWaiting("a", 1)

	require.NoError(t, h.tick())
	reserve, release := h.stub.Calls()
	assert.Zero(t, reserve)
	assert.Zero(t, release)
	assert.Equal(t, "WAITING", h.status("a"))
}

// QSA-7.5 / A-6: lowering capacity below the current active count admits none and revokes none.
func TestPromoter_LoweringCapacityBelowActiveAdmitsNoneRevokesNone(t *testing.T) {
	ms := &mutableSettings{rows: map[string]entity.EventSettings{"ev": {EventID: "ev", ActiveSlotCapacity: 3}}}
	h := newMutableHarness(t, ms, 10)
	h.store.addWaiting("a", 1)
	h.store.addWaiting("b", 2)
	h.mustTick()
	require.Equal(t, "ADMITTED", h.status("a"))
	require.Equal(t, "ADMITTED", h.status("b"))

	ms.put(entity.EventSettings{EventID: "ev", ActiveSlotCapacity: 1}) // below the 2 active
	h.store.addWaiting("c", 3)
	reserveBefore, _ := h.stub.Calls()
	h.mustTick()
	h.mustTick()

	assert.Equal(t, "WAITING", h.status("c"), "tidak ada yang diadmit sampai active < kapasitas")
	assert.Equal(t, "ADMITTED", h.status("a"), "tidak ada entry aktif yang dicabut")
	assert.Equal(t, "ADMITTED", h.status("b"))
	reserveAfter, release := h.stub.Calls()
	assert.Equal(t, reserveBefore, reserveAfter, "tidak ada reservasi Booking saat free <= 0")
	assert.Zero(t, release)
}

// QSA-7.3: an event whose row is created after the worker started is visited on the next tick.
func TestPromoter_NewlyConfiguredEventIsVisitedNextTick(t *testing.T) {
	ms := &mutableSettings{rows: map[string]entity.EventSettings{}}
	h := newMutableHarness(t, ms, 10)
	h.store.addWaiting("a", 1)

	h.mustTick()
	assert.Equal(t, "WAITING", h.status("a"), "belum dikonfigurasi: tidak disentuh")

	ms.put(entity.EventSettings{EventID: "ev", ActiveSlotCapacity: 2})
	h.mustTick()
	assert.Equal(t, "ADMITTED", h.status("a"))
}
