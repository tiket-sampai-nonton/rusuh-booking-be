//go:build integration

package valkey

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// SRS-F-10-02 / req 5.5: booking.sold_out ends WAITING and STALE entries with SOLD_OUT, never ADMITTED/CLAIMED
// (Jangan PHP), idempotently, in bounded batches.
func TestEndWaitingSoldOut_EndsWaitingAndStaleButNeverAllocated(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	ev := evID(t)
	now := time.Now().UnixMilli()

	adm, _, _ := joinAdmit(t, s, ev, 1, now+60_000, now)
	claimed, visC, _ := joinAdmit(t, s, ev, 2, now+60_000, now)
	cl, err := s.Claim(ctx, ev, claimed, visC, now)
	require.NoError(t, err)
	require.Equal(t, "CLAIMED", cl.Status)
	w1, visW1, nikW1 := joinOne(t, s, ev, 3, now)
	w2, _, _ := joinOne(t, s, ev, 4, now)
	stale, _, _ := joinOne(t, s, ev, 5, now-10_000)

	// make `stale` STALE through the real sweep (silent since now-10s; w1/w2/others heartbeat later)
	tok := lease(t, s, ev)
	r, err := s.SweepLiveness(ctx, ev, tok, now-5_000, now-1_000_000, 100, now)
	require.NoError(t, err)
	require.EqualValues(t, 1, r.Staled)
	require.Equal(t, "STALE", status(t, s, ev, stale))

	res, err := s.EndWaitingSoldOut(ctx, ev, now, 100)
	require.NoError(t, err)
	assert.Equal(t, 3, res.Scanned)
	assert.Equal(t, 3, res.Ended)
	for _, id := range []string{w1, w2, stale} {
		assert.Equal(t, "SOLD_OUT", status(t, s, ev, id))
		assert.Equal(t, "SOLD_OUT", hget(t, s, s.kEntry(ev, id), "endReason"))
		_, inWaiting := zscore(t, s, s.kWaiting(ev), id)
		assert.False(t, inWaiting)
		_, inStale := zscore(t, s, s.k(ev, "stale"), id)
		assert.False(t, inStale)
	}
	constraintKeysGone(t, s, ev, nikW1, visW1, 3)
	assert.Equal(t, "ADMITTED", status(t, s, ev, adm), "ADMITTED tidak disentuh (Jangan PHP)")
	assert.Equal(t, "CLAIMED", status(t, s, ev, claimed), "CLAIMED tidak disentuh (Jangan PHP)")
	_, inActive := zscore(t, s, s.kActive(ev), adm)
	assert.True(t, inActive)

	// duplicate delivery: nothing left, nothing changes
	res, err = s.EndWaitingSoldOut(ctx, ev, now+1, 100)
	require.NoError(t, err)
	assert.Equal(t, 0, res.Scanned)
	assert.Equal(t, 0, res.Ended)
	assert.Equal(t, "SOLD_OUT", status(t, s, ev, w1))
	assert.Equal(t, "ADMITTED", status(t, s, ev, adm))
}

func TestEndWaitingSoldOut_IsBatched(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	ev := evID(t)
	now := time.Now().UnixMilli()
	const total, batch = 25, 10
	ids := make([]string, 0, total)
	for i := 1; i <= total; i++ {
		id, _, _ := joinOne(t, s, ev, i, now)
		ids = append(ids, id)
	}
	var scanned []int
	for {
		res, err := s.EndWaitingSoldOut(ctx, ev, now, batch)
		require.NoError(t, err)
		scanned = append(scanned, res.Scanned)
		if res.Scanned < batch {
			break
		}
	}
	assert.Equal(t, []int{10, 10, 5}, scanned)
	for _, id := range ids {
		assert.Equal(t, "SOLD_OUT", status(t, s, ev, id), fmt.Sprintf("entry %s", id))
	}
	_, err := s.EndWaitingSoldOut(ctx, ev, now, 0)
	assert.Error(t, err, "batch harus positif")
}
