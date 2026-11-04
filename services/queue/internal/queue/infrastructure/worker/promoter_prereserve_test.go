package worker_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/infrastructure/worker"
)

// TC-QUE-19g orphan head (C5): an id in waiting without an entry hash is ended at once and never sent to Booking.
// Before this fix the promoter `continue`d forever, so with free == 1 a single orphan blocked the whole queue.
func TestPromoter_OrphanHeadIsEndedInOneTickAndNeverReserved(t *testing.T) {
	h := newHarness(t, 1, 5, worker.PromoterTuning{})
	h.store.addOrphanWaiting("ghost", 1)
	h.store.addWaiting("e2", 2)

	h.mustTick()
	assert.NotContains(t, h.waiting(), "ghost", "id yatim dihapus dari waiting")
	assert.Equal(t, 1, h.metrics.Count(worker.MetricEntryEnded, "outcome", "PROMOTER_ORPHAN"))
	reserve, _ := h.stub.Calls()
	assert.Zero(t, reserve, "tidak ada ReserveBatch untuk id yatim")

	h.mustTick()
	assert.Equal(t, "ADMITTED", h.status("e2"), "tick berikutnya entry di belakangnya diadmit")
}

// TC-QUE-19h unreadable PII, entry-scoped (C6): a sibling opens fine, so H is counted per tick and ended exactly when
// the counter reaches UnsealFailMax; the sibling is promoted on tick 1 and new siblings keep joining so the probe
// always has something that opens (otherwise, once alone, H is indistinguishable from the systemic case by design).
func TestPromoter_UnreadablePIIEntryScopedEndsAtTheThreshold(t *testing.T) {
	h := newHarness(t, 2, 5, worker.PromoterTuning{UnsealFailMax: 3})
	h.store.addWaitingWithEmail("h", 1, "bad")
	h.store.addWaiting("e2", 2)

	h.mustTick()
	assert.Equal(t, "ADMITTED", h.status("e2"), "sibling dipromosikan di tick 1")
	assert.Equal(t, "WAITING", h.status("h"))
	h.store.addWaiting("e3", 3) // keeps a sibling available for the probe on later ticks
	h.mustTick()
	assert.Equal(t, "WAITING", h.status("h"), "belum diakhiri sebelum ambang")
	h.store.addWaiting("e4", 4)
	h.mustTick()
	assert.Equal(t, "ENDED", h.status("h"), "diakhiri tepat di tick ke-3")
	assert.Equal(t, 3, h.metrics.Count(worker.MetricUnsealFailed, "scope", "entry"))
	assert.Equal(t, 1, h.metrics.Count(worker.MetricEntryEnded, "outcome", "UNREADABLE_PII"))
}

// TC-QUE-19i systemic (QSH-OQ-16): when no id opens (mis-set key) nothing is ended, however many ticks pass.
func TestPromoter_UnreadablePIISystemicEndsNothing(t *testing.T) {
	h := newHarness(t, 1, 5, worker.PromoterTuning{UnsealFailMax: 2, UnsealProbe: 3})
	h.pii.allBad = true
	for i, id := range []string{"a", "b", "c", "d", "e"} {
		h.store.addWaiting(id, byte(i+1))
	}
	for i := 0; i < 5; i++ {
		h.mustTick()
	}
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		assert.Equal(t, "WAITING", h.status(id), "tidak ada yang diakhiri saat kegagalan sistemik")
	}
	assert.Equal(t, 5, h.metrics.Count(worker.MetricUnsealFailed, "scope", "systemic"))
	assert.Zero(t, h.metrics.Count(worker.MetricEntryEnded, "outcome", "UNREADABLE_PII"))
	reserve, _ := h.stub.Calls()
	assert.Zero(t, reserve)
	require.Zero(t, h.metrics.Count(worker.MetricUnsealFailed, "scope", "entry"))
}
