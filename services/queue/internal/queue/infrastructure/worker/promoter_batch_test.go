package worker_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/infrastructure/worker"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// TC-QUE-19d (P18, reviewer reproduction as a negative test): a FULL/NOT_HEAD-after-grant deferral cannot leave a
// stranded row, because the lease is held across reserve+promote (L1). The old rev-3 bug would strand E's row and
// end it later via ALREADY_ACTIVE; this must never happen now: while free == 0, E is never even sent to Booking.
func TestPromoter_NoFreeSlotNeverReservesAndNeverStrandsARow(t *testing.T) {
	h := newHarness(t, 1, 5, worker.PromoterTuning{})
	h.store.addWaiting("other", 1)
	h.mustTick()
	require.Equal(t, "ADMITTED", h.status("other"))

	h.store.addWaiting("e", 2)
	for i := 0; i < 10; i++ {
		h.mustTick()
	}
	reserve, _ := h.stub.Calls()
	assert.Equal(t, 1, reserve, "e tidak pernah dikirim ke Booking selagi free == 0")
	assert.Equal(t, "WAITING", h.status("e"))

	h.store.freeSlot("other") // the slot frees
	h.mustTick()
	assert.Equal(t, "ADMITTED", h.status("e"), "begitu slot bebas, e digranted dan diadmit")
	_, release := h.stub.Calls()
	assert.Equal(t, 0, release, "e tidak pernah diakhiri atau dilepas")
}

// TC-QUE-19e interrupted tick: Promote fails k times, then succeeds inside the claim window, admitting with the
// same allocation id; EndEntry is never called.
func TestPromoter_InterruptedTickReplaysSameAllocationAndNeverEnds(t *testing.T) {
	h := newHarness(t, 1, 5, worker.PromoterTuning{})
	h.store.addWaiting("e", 1)
	h.store.promoteErr = assertErr
	for i := 0; i < 3; i++ {
		require.Error(t, h.tick())
	}
	h.store.promoteErr = nil
	h.mustTick()
	assert.Equal(t, "ADMITTED", h.status("e"))
	firstAlloc := h.allocOf("e")
	require.NotEmpty(t, firstAlloc)

	reserve, release := h.stub.Calls()
	assert.Equal(t, 4, reserve, "setiap tick yang terinterupsi tetap memanggil ReserveBatch (replay)")
	assert.Equal(t, 0, release)

	// Outage past expiresAt + stub sweep ⇒ the row becomes dead; the next tick sees ALREADY_ACTIVE and ends it once.
	h2 := newHarness(t, 1, 5, worker.PromoterTuning{})
	h2.store.addWaiting("e2", 1)
	h2.store.promoteErr = assertErr
	h2.mustTickErr()
	h2.store.promoteErr = nil
	h2.clk.T = h2.clk.T.Add(10 * time.Minute)
	h2.stub.SweepSessions()
	h2.mustTick()
	assert.Equal(t, "ENDED", h2.status("e2"))
	assert.Equal(t, 1, h2.metrics.Count(worker.MetricEntryEnded, "outcome", "ALREADY_ACTIVE"))
}

// TC-QUE-19f dead-head bound (T2): a head whose Booking row is dead is ended within one tick, and the entry behind
// it in the same batch is admitted in that same tick (the dead head only consumed one candidate position).
func TestPromoter_DeadHeadEndedWithinOneTickWhileSiblingsAdmit(t *testing.T) {
	h := newHarness(t, 3, 5, worker.PromoterTuning{})
	h.store.addWaiting("e1", 1)
	h.store.addWaiting("e2", 2)
	h.mustTick() // e1 and e2 get real rows and are admitted (capacity 3)
	require.Equal(t, "ADMITTED", h.status("e1"))
	require.Equal(t, "ADMITTED", h.status("e2"))
	require.NoError(t, h.stub.ReleaseAllocation(ctxTODO(), h.allocOf("e1"), "x")) // e1's Booking row is now dead (RELEASED)
	h.store.freeSlot("e1")
	h.store.addWaitingAt("e1", 1) // e1 is WAITING at the Queue and head again
	h.store.addWaiting("e3", 3)

	h.mustTick() // free == 2: candidates [e1 (dead), e3]
	assert.Equal(t, "ENDED", h.status("e1"))
	assert.NotContains(t, h.waiting(), "e1")
	assert.Equal(t, "ADMITTED", h.status("e3"))
	assert.Equal(t, 1, h.metrics.Count(worker.MetricEntryEnded, "outcome", "ALREADY_ACTIVE"))
	reserve1, _ := h.stub.Calls()
	h.mustTick()
	reserve2, _ := h.stub.Calls()
	assert.Equal(t, reserve1, reserve2, "e1 tidak pernah dikirim lagi setelah diakhiri")
}

// With free == 1 a dead head consumes the only candidate position: ended this tick, the next entry is admitted on the
// following tick (documented in design §5.4a.3 T2).
func TestPromoter_DeadHeadWithOneFreeSlotEndsThenNextTickAdmitsTheNext(t *testing.T) {
	h := newHarness(t, 1, 5, worker.PromoterTuning{})
	h.store.addWaiting("e1", 1)
	h.mustTick()
	require.Equal(t, "ADMITTED", h.status("e1"))
	require.NoError(t, h.stub.ReleaseAllocation(ctxTODO(), h.allocOf("e1"), "x"))
	h.store.freeSlot("e1")
	h.store.addWaitingAt("e1", 1)
	h.store.addWaiting("e2", 2)

	h.mustTick()
	assert.Equal(t, "ENDED", h.status("e1"))
	assert.Equal(t, "WAITING", h.status("e2"))
	h.mustTick()
	assert.Equal(t, "ADMITTED", h.status("e2"))
}

var assertErr = &stubErr{"promote gagal"}

type stubErr struct{ msg string }

func (e *stubErr) Error() string { return e.msg }

// TC-QUE-19k (the reviewer's reproduction as a regression test, run against the correct stub). Stock returns between
// two entries of one Booking batch (the commit in the savepoint gap), so the batch is [NO_SUPPLY_NOW(E1), GRANTED(E2)].
// The old promoter released E2 (the jumper release) => RELEASED row => ALREADY_ACTIVE forever => the Queue ended a live
// buyer. Adoption must admit E2 and never end or release it.
func TestPromoter_StockReturnedMidBatchAdoptsTheLaterEntryAndNeverEndsIt(t *testing.T) {
	h := newHarness(t, 2, 0, worker.PromoterTuning{})
	h.store.addWaiting("e1", 1)
	h.store.addWaiting("e2", 2)
	h.stub.afterEntry(0, func() { h.stub.stock++ }) // a unit of stock comes back right after entry 1 was refused

	h.mustTick()
	assert.Equal(t, "ADMITTED", h.status("e2"), "e2 diadopsi")
	assert.NotEmpty(t, h.allocOf("e2"))
	assert.Equal(t, "WAITING", h.status("e1"), "e1 tetap head, tanpa baris Booking")
	assert.Equal(t, []string{"e1"}, h.waiting())
	_, release := h.stub.Calls()
	assert.Equal(t, 0, release, "tidak ada ReleaseAllocation (release jumper dihapus)")
	assert.Equal(t, 1, h.metrics.Count(worker.MetricFifoAdopted, "suspect", "false"))

	// Tick 2, then the claim window passes and Booking's sweeper runs: e2 must never be ended or answered ALREADY_ACTIVE.
	h.mustTick()
	h.clk.T = h.clk.T.Add(4 * time.Minute)
	h.stub.SweepSessions()
	h.mustTick()
	assert.Equal(t, "ADMITTED", h.status("e2"), "e2 tidak pernah diakhiri oleh Queue")
	assert.Zero(t, h.metrics.Count(worker.MetricEntryEnded, "outcome", "ALREADY_ACTIVE"))

	// Stock returns again: e1 is granted and admitted.
	h.stub.stock++
	h.mustTick()
	assert.Equal(t, "ADMITTED", h.status("e1"))
}

// TC-QUE-19b adoption: [NO_SUPPLY_NOW, GRANTED, GRANTED] promotes both later entries with zero releases.
func TestPromoter_AdoptsEveryGrantAfterAPoisonHead(t *testing.T) {
	h := newHarness(t, 3, 3, worker.PromoterTuning{})
	h.stub.failEntry("e1")
	h.store.addWaiting("e1", 1)
	h.store.addWaiting("e2", 2)
	h.store.addWaiting("e3", 3)
	h.mustTick()
	assert.Equal(t, "ADMITTED", h.status("e2"))
	assert.Equal(t, "ADMITTED", h.status("e3"))
	assert.Equal(t, "WAITING", h.status("e1"))
	_, release := h.stub.Calls()
	assert.Zero(t, release)
	assert.Equal(t, 2, h.metrics.Count(worker.MetricFifoAdopted, "suspect", "false"))
}

// TC-QUE-19j lapsed replay (C7, rule 3a): a replayed GRANTED whose deadline has passed is skipped with no mutation,
// no release and no FIFO flag; later fresh grants in the same batch are promoted normally.
func TestPromoter_LapsedReplayIsSkippedWithoutMutationAndDoesNotBlockLaterGrants(t *testing.T) {
	h := newHarness(t, 2, 5, worker.PromoterTuning{})
	h.store.addWaiting("e1", 1)
	// e1 gets a row first (tick with the store refusing Promote), then time passes beyond its expiry, un-swept.
	h.store.promoteErr = assertErr
	require.Error(t, h.tick())
	h.store.promoteErr = nil
	h.clk.T = h.clk.T.Add(4 * time.Minute) // past the 3 minute claim window, sweeper has NOT run
	h.store.addWaiting("e2", 2)

	h.mustTick() // batch [e1 lapsed replay, e2 fresh]
	assert.Equal(t, "WAITING", h.status("e1"), "e1 tidak dipromosikan dengan deadline lampau")
	assert.Equal(t, "ADMITTED", h.status("e2"), "grant segar di belakang dilewati? tidak: dipromosikan normal")
	_, release := h.stub.Calls()
	assert.Zero(t, release, "tidak ada release untuk lapsed maupun untuk e2")
	assert.Equal(t, 1, h.metrics.Count(worker.MetricLapsedReplay))
	assert.Zero(t, h.metrics.Count(worker.MetricFifoAdopted, "suspect", "false"), "lapsed bukan NO_SUPPLY_NOW")
	assert.Zero(t, h.metrics.Count(worker.MetricEntryEnded, "outcome", "ALREADY_ACTIVE"))

	// A-1 negative: the sweeper never runs ⇒ skipped again on every tick, never ended (counter is the alert signal).
	h.mustTick()
	h.mustTick()
	assert.Equal(t, 3, h.metrics.Count(worker.MetricLapsedReplay))
	assert.Equal(t, "WAITING", h.status("e1"))

	// Booking's sweeper finally runs: next tick sees ALREADY_ACTIVE and ends e1 once.
	h.stub.SweepSessions()
	h.mustTick()
	assert.Equal(t, "ENDED", h.status("e1"))
	assert.Equal(t, 1, h.metrics.Count(worker.MetricEntryEnded, "outcome", "ALREADY_ACTIVE"))
}

func TestPromoter_LapsedHeadWithOneFreeSlotIsANoOpTick(t *testing.T) {
	h := newHarness(t, 1, 5, worker.PromoterTuning{})
	h.store.addWaiting("e1", 1)
	h.store.promoteErr = assertErr
	require.Error(t, h.tick())
	h.store.promoteErr = nil
	h.store.addWaiting("e2", 2)
	h.clk.T = h.clk.T.Add(4 * time.Minute)

	h.mustTick()
	assert.Equal(t, "WAITING", h.status("e1"))
	assert.Equal(t, "WAITING", h.status("e2"), "free == 1 dan head lapsed: tick tanpa mutasi")
	h.stub.SweepSessions()
	h.mustTick()
	assert.Equal(t, "ENDED", h.status("e1"))
	h.mustTick()
	assert.Equal(t, "ADMITTED", h.status("e2"))
}

// Unknown Booking outcome: counted and no mutation; the rest of the batch is handled normally.
func TestPromoter_UnknownOutcomeIsCountedAndMutatesNothing(t *testing.T) {
	h := newHarness(t, 2, 5, worker.PromoterTuning{})
	h.store.addWaiting("e1", 1)
	h.store.addWaiting("e2", 2)
	wrapped := &unknownOutcomeBooking{inner: h.stub, entry: "e1"}
	p := worker.NewPromoter(h.store, h.settings, wrapped, h.pub, fakeAudit{}, h.clk, nil, worker.PromoterTuning{BatchMax: 200}).WithMetrics(h.metrics)
	require.NoError(t, p.Run(ctxTODO()))
	assert.Equal(t, 1, h.metrics.Count(worker.MetricUnknownOutcome))
	assert.Equal(t, "WAITING", h.status("e1"))
	assert.Equal(t, "ADMITTED", h.status("e2"))
}

// TC-C8.8: the candidates of one tick are read in one GetEntries call, never one GetEntry per candidate (PF-P2-2).
func TestPromoter_FetchesCandidatesInOneCall(t *testing.T) {
	h := newHarness(t, 10, 0, worker.PromoterTuning{}) // no stock: every candidate answers NO_SUPPLY_NOW
	for i := 1; i <= 5; i++ {
		h.store.addWaiting(fmt.Sprintf("e%d", i), 0)
	}
	h.mustTick()
	assert.Equal(t, 1, h.store.batchReads, "all candidates come back in a single GetEntries call")
	assert.Equal(t, 0, h.store.entryReads, "no per-candidate GetEntry on the promoter path")
}

// C8.8: a failed batch read skips the whole tick: nothing is reserved at Booking and no candidate is ended, since a
// transient read failure is not an orphan (C5).
func TestPromoter_FailedBatchReadSkipsTheTickAndEndsNothing(t *testing.T) {
	h := newHarness(t, 10, 5, worker.PromoterTuning{})
	h.store.addWaiting("e1", 0)
	h.store.addOrphanWaiting("orphan", 99)
	h.store.batchErr = errors.New("valkey down")
	h.mustTick()
	reserve, _ := h.stub.Calls()
	assert.Equal(t, 0, reserve, "no candidate is sent to Booking")
	assert.Equal(t, "WAITING", h.status("e1"))
	assert.Equal(t, []string{"e1", "orphan"}, h.waiting(), "the orphan is not ended on a read failure")
}

// leaseCheckPub records each turn.arrived publish in the store's call log and fails the test when the promoter lease
// is still held at that moment (C8.8: announcements leave the lease).
type leaseCheckPub struct {
	*fakePub
	store *fakeStore
	t     *testing.T
}

func (p leaseCheckPub) PublishTurnArrived(ctx context.Context, ev, entryID, visitorID string, deadline time.Time) error {
	p.store.mu.Lock()
	held := p.store.leaseToken != ""
	p.store.mu.Unlock()
	if held {
		p.t.Errorf("turn.arrived %s published while the promoter lease is held", entryID)
	}
	p.store.record("arrived:" + entryID)
	return p.fakePub.PublishTurnArrived(ctx, ev, entryID, visitorID, deadline)
}

// TC-C8.8: turn.arrived is published after the lease is released, not inside it.
func TestPromoter_PublishesAfterLeaseRelease(t *testing.T) {
	h := newHarness(t, 2, 5, worker.PromoterTuning{})
	h.store.addWaiting("a", 0)
	h.store.addWaiting("b", 0)
	p := worker.NewPromoter(h.store, h.settings, h.stub, leaseCheckPub{fakePub: h.pub, store: h.store, t: t}, fakeAudit{}, h.clk, slog.New(slog.NewTextHandler(discard{}, nil)), worker.PromoterTuning{BatchMax: 200})
	require.NoError(t, p.Run(context.Background()))
	assert.Equal(t, []string{"release", "arrived:a", "arrived:b"}, h.store.order)
	assert.Equal(t, []string{"a", "b"}, h.pub.Arrived())
}

var _ outbound.IEventPublisher = leaseCheckPub{}
