package worker_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/infrastructure/worker"
)

// completeAdmitted finishes every ADMITTED entry (models PAID) so the next tick has free slots again.
func (h *harness) completeAdmitted(ids ...string) {
	for _, id := range ids {
		h.stub.complete(id)
		h.store.freeSlot(id)
	}
}

// TC-QUE-19l (1)(2): a poison head P (Booking answers NO_SUPPLY_NOW with no row, stock available) is never ended or
// released, the entry behind it is adopted each tick, and from tick N the page-worthy series rise.
func TestPromoter_PoisonHeadIsNeverEndedAndSuspectSeriesRiseFromTickN(t *testing.T) {
	const n = 3
	h := newHarness(t, 2, 100, worker.PromoterTuning{NoSupplySuspectTicks: n})
	h.stub.failEntry("P")
	h.store.addWaiting("P", 1)
	seq := byte(2)
	var prev string
	for tick := 1; tick <= n+4; tick++ {
		if prev != "" {
			h.completeAdmitted(prev) // free == 2 again before this tick
		}
		next := "E" + string('a'+byte(tick))
		h.store.addWaiting(next, seq)
		seq++
		h.mustTick()
		assert.Equal(t, "WAITING", h.status("P"), "tick %d: P tidak pernah diakhiri", tick)
		assert.Equal(t, []string{"P"}, h.waiting(), "tick %d: P tetap head di waiting", tick)
		assert.Equal(t, "ADMITTED", h.status(next), "tick %d: entry di belakang P diadopsi", tick)
		prev = next
		wantRepeat := 0
		if tick >= n {
			wantRepeat = tick - n + 1
		}
		assert.Equal(t, wantRepeat, h.metrics.Count(worker.MetricHeadNoSupply, "adopted", "true"), "tick %d", tick)
		assert.Zero(t, h.metrics.Count(worker.MetricHeadNoSupply, "adopted", "false"), "tick %d", tick)
		assert.Equal(t, wantRepeat, h.metrics.Count(worker.MetricFifoAdopted, "suspect", "true"), "tick %d", tick)
	}
	_, release := h.stub.Calls()
	assert.Zero(t, release)
	assert.Equal(t, 0, h.metrics.Count(worker.MetricEntryEnded, "outcome", "ALREADY_ACTIVE"))

	// (3) clearFail: liveness is restored by fixing the cause, with no Queue change.
	h.stub.clearFail("P")
	h.completeAdmitted(prev)
	h.store.addWaiting("Z", seq)
	h.mustTick()
	assert.Equal(t, "ADMITTED", h.status("P"))
}

// TC-QUE-19l (4): free == 1 variant. No adoption is possible, so only adopted="false" rises, from tick N on.
func TestPromoter_PoisonHeadWithOneFreeSlotRaisesOnlyAdoptedFalse(t *testing.T) {
	const n = 3
	h := newHarness(t, 1, 100, worker.PromoterTuning{NoSupplySuspectTicks: n})
	h.stub.failEntry("P")
	h.store.addWaiting("P", 1)
	for tick := 1; tick <= n+2; tick++ {
		h.mustTick()
		assert.Equal(t, "WAITING", h.status("P"))
		want := 0
		if tick >= n {
			want = tick - n + 1
		}
		assert.Equal(t, want, h.metrics.Count(worker.MetricHeadNoSupply, "adopted", "false"), "tick %d", tick)
		assert.Zero(t, h.metrics.Count(worker.MetricHeadNoSupply, "adopted", "true"))
		assert.Zero(t, h.metrics.Count(worker.MetricFifoAdopted, "suspect", "true"))
	}
}

// TC-QUE-19l (5a): an ordinary stock-return race is not flagged (N >= 2).
func TestPromoter_OrdinaryStockRaceIsNotFlaggedAsSuspect(t *testing.T) {
	h := newHarness(t, 2, 0, worker.PromoterTuning{NoSupplySuspectTicks: 3})
	h.store.addWaiting("P", 1)
	h.store.addWaiting("E2", 2)
	h.stub.afterEntry(0, func() { h.stub.stock++ }) // race on tick 1 only
	h.mustTick()
	assert.Equal(t, "ADMITTED", h.status("E2"))
	h.stub.afterEntry(0, func() {}) // no more race
	h.stub.stock = 1
	h.mustTick()
	assert.Equal(t, "ADMITTED", h.status("P"), "P dilayani tick berikutnya")
	assert.Zero(t, h.metrics.Count(worker.MetricHeadNoSupply, "adopted", "true"))
	assert.Zero(t, h.metrics.Count(worker.MetricHeadNoSupply, "adopted", "false"))
	assert.Zero(t, h.metrics.Count(worker.MetricFifoAdopted, "suspect", "true"))
}

// TC-QUE-19l (5b): pins the rejection of "end the head after N adopted ticks": an adversarial race for N+5 ticks never
// ends the head, and because it is indistinguishable from poison the suspect series do rise.
func TestPromoter_AdversarialStockRaceNeverEndsTheHead(t *testing.T) {
	const n = 3
	h := newHarness(t, 2, 0, worker.PromoterTuning{NoSupplySuspectTicks: n})
	h.stub.afterEntry(0, func() { h.stub.stock++ }) // stock returns right after the head was refused, every tick
	h.store.addWaiting("P", 1)
	var prev string
	for tick := 1; tick <= n+5; tick++ {
		if prev != "" {
			h.completeAdmitted(prev)
		}
		next := "R" + string('a'+byte(tick))
		h.store.addWaiting(next, byte(tick+1))
		h.mustTick()
		prev = next
	}
	assert.Equal(t, "WAITING", h.status("P"), "o Queue tidak pernah mengakhiri head")
	assert.Positive(t, h.metrics.Count(worker.MetricFifoAdopted, "suspect", "true"))
	assert.Zero(t, h.metrics.Count(worker.MetricEntryEnded, "outcome", "ALREADY_ACTIVE"))
}

// TC-QUE-19l (10) memory bound: the record is dropped when waiting is empty.
func TestPromoter_RecordIsDroppedWhenNothingIsWaiting(t *testing.T) {
	h := newHarness(t, 1, 100, worker.PromoterTuning{NoSupplySuspectTicks: 3})
	h.stub.failEntry("P")
	h.store.addWaiting("P", 1)
	h.mustTick()
	h.mustTick()
	h.store.removeOrphanFromWaiting("P") // the head leaves
	h.mustTick()                         // empty waiting: row 0, record dropped
	h.store.addWaiting("P2", 5)
	h.stub.failEntry("P2")
	h.mustTick()
	h.mustTick()
	assert.Zero(t, h.metrics.Count(worker.MetricHeadNoSupply, "adopted", "false"), "run P2 mulai dari nol, tidak mewarisi hitungan P")
}
