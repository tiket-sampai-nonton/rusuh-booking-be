package worker_test

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/infrastructure/worker"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// fakeMetrics records every counter increment (fakemetrics_test.go in the plan).
type fakeMetrics struct {
	mu     sync.Mutex
	counts map[string]int
}

func newFakeMetrics() *fakeMetrics { return &fakeMetrics{counts: map[string]int{}} }

func key(name string, labels []string) string {
	k := name
	for _, l := range labels {
		k += "|" + l
	}
	return k
}

func (m *fakeMetrics) Inc(name string, labels ...string) {
	m.mu.Lock()
	m.counts[key(name, labels)]++
	m.mu.Unlock()
}

// Count returns the counter for name with the given alternating label key/value pairs.
func (m *fakeMetrics) Count(name string, labels ...string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.counts[key(name, labels)]
}

type fakeSettings struct {
	s entity.EventSettings
}

func (f *fakeSettings) Get(context.Context, string) (entity.EventSettings, bool, error) {
	return f.s, true, nil
}
func (f *fakeSettings) ListEventIDs(context.Context) ([]string, error)               { return []string{"ev"}, nil }
func (f *fakeSettings) Ensure(context.Context, string) (entity.EventSettings, error) { return f.s, nil }

type fakePub struct {
	mu      sync.Mutex
	arrived []string
}

func (p *fakePub) PublishTurnSoon(context.Context, string, string, string) error { return nil }
func (p *fakePub) PublishTurnArrived(_ context.Context, _, entryID, _ string, _ time.Time) error {
	p.mu.Lock()
	p.arrived = append(p.arrived, entryID)
	p.mu.Unlock()
	return nil
}
func (p *fakePub) PublishJourneyEnded(context.Context, string, string, string) error { return nil }
func (p *fakePub) Arrived() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string{}, p.arrived...)
}

// fakePII fails to open any email whose ciphertext is "bad" (entry-scoped) or every email when allBad is set.
type fakePII struct{ allBad bool }

func (f *fakePII) HashNIK(string) []byte            { return nil }
func (f *fakePII) HashEmail(string) []byte          { return nil }
func (f *fakePII) SealEmail(string) ([]byte, error) { return []byte("ok"), nil }
func (f *fakePII) SealBuyerEmail(string) (string, error) {
	return "c2VhbGVk", nil
}
func (f *fakePII) UnsealEmail(enc []byte) (string, error) {
	if f.allBad || string(enc) == "bad" {
		return "", errors.New("unseal gagal")
	}
	return "pembeli@contoh.id", nil
}

type fakeAudit struct{}

func (fakeAudit) Append(context.Context, outbound.AuditRecord) error { return nil }

// harness wires the real Promoter to the fake store, the correct Booking stub, fake metrics and publisher.
type harness struct {
	t        *testing.T
	store    *fakeStore
	stub     *bookingStub
	metrics  *fakeMetrics
	pub      *fakePub
	pii      *fakePII
	settings *fakeSettings
	clk      *clock.Fixed
	p        *worker.Promoter
}

func newHarness(t *testing.T, capacity, stock int, tuning worker.PromoterTuning) *harness {
	t.Helper()
	h := &harness{t: t, store: newFakeStore(), metrics: newFakeMetrics(), pub: &fakePub{}, pii: &fakePII{}}
	h.clk = &clock.Fixed{T: time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)}
	h.stub = newBookingStub(h.clk, 3*time.Minute, stock)
	h.settings = &fakeSettings{s: entity.EventSettings{EventID: "ev", ActiveSlotCapacity: capacity}}
	if tuning.BatchMax == 0 {
		tuning.BatchMax = 200
	}
	h.p = worker.NewPromoter(h.store, h.settings, h.stub, h.pub, fakeAudit{}, h.clk, slog.New(slog.NewTextHandler(discard{}, nil)), tuning).WithMetrics(h.metrics)
	return h
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

func (h *harness) tick() error  { return h.p.Run(context.Background()) }
func (h *harness) mustTick()    { h.t.Helper(); require.NoError(h.t, h.tick()) }
func (h *harness) mustTickErr() { h.t.Helper(); require.Error(h.t, h.tick()) }

func (h *harness) status(id string) string {
	e, ok, _ := h.store.GetEntry(context.Background(), "ev", id)
	if !ok {
		return "<none>"
	}
	return string(e.Status)
}

func (h *harness) allocOf(id string) string {
	e, _, _ := h.store.GetEntry(context.Background(), "ev", id)
	return e.AllocationID
}

func (h *harness) waiting() []string {
	ids, _ := h.store.TopWaiting(context.Background(), "ev", 100)
	return ids
}
