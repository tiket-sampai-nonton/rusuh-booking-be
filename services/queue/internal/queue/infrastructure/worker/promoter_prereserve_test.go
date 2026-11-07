package worker_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

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
