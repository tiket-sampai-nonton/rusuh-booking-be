//go:build integration

package valkey

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

func joinOne(t *testing.T, s *QueueStore, ev string, n int, nowMs int64) (entryID, visitorID, nikHash string) {
	t.Helper()
	entryID, visitorID, nikHash = fmt.Sprintf("entry-%d", n), fmt.Sprintf("visitor-%d", n), fmt.Sprintf("%064x", n)
	res, err := s.Join(context.Background(), outbound.JoinParams{
		EventID: ev, EntryID: entryID, VisitorID: visitorID, NikHash: nikHash, EmailHashHex: fmt.Sprintf("%064x", n+1000),
		EmailEnc: []byte("sealed"), NikMasked: "3174********0001", NowMs: nowMs, OneActivePerVisitor: true, OneActivePerEmail: true,
	})
	require.NoError(t, err)
	require.Equal(t, outbound.JoinOK, res.Outcome)
	return
}

// The whole life of a queue entry: waiting -> admitted -> claimed, missed turns freeing the slot, leaving, and the
// bookkeeping the Promotor and Sweeper rely on.
func TestQueueStore_EntryLifecycle(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	ev := "evt-life"
	now := time.Now().UnixMilli()

	a, visA, _ := joinOne(t, s, ev, 1, now)
	b, visB, _ := joinOne(t, s, ev, 2, now)
	c, visC, nikC := joinOne(t, s, ev, 3, now)

	top, err := s.TopWaiting(ctx, ev, 10)
	require.NoError(t, err)
	assert.Equal(t, []string{a, b, c}, top, "FIFO menurut seq")
	none, err := s.TopWaiting(ctx, ev, 0)
	require.NoError(t, err)
	assert.Empty(t, none)
	active, err := s.ActiveCount(ctx, ev)
	require.NoError(t, err)
	assert.Equal(t, 0, active)

	// Promote two with different claim deadlines; capacity 2 admits both and refuses a third.
	promoted, err := s.Promote(ctx, ev, []outbound.PromoteCandidate{
		{EntryID: a, AllocationID: "alloc-a", ClaimDeadlineMs: now + 60_000},
		{EntryID: b, AllocationID: "alloc-b", ClaimDeadlineMs: now - 1_000}, // already past: will be missed
	}, 2, now)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{a, b}, promoted)
	full, err := s.Promote(ctx, ev, []outbound.PromoteCandidate{{EntryID: c, AllocationID: "alloc-c", ClaimDeadlineMs: now + 60_000}}, 2, now)
	require.NoError(t, err)
	assert.Empty(t, full, "slot penuh")

	entA, found, err := s.GetEntry(ctx, ev, a)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "ADMITTED", string(entA.Status))
	assert.Equal(t, "alloc-a", entA.AllocationID)

	byVisitor, found, err := s.FindByVisitor(ctx, ev, visA)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, a, byVisitor.ID)
	_, found, err = s.FindByVisitor(ctx, ev, "visitor-tak-dikenal")
	require.NoError(t, err)
	assert.False(t, found)

	// Claim: only the owner, and only before the deadline.
	_, err = s.Claim(ctx, ev, a, "visitor-lain", now)
	assert.ErrorIs(t, err, entity.ErrNotFound)
	claim, err := s.Claim(ctx, ev, a, visA, now)
	require.NoError(t, err)
	assert.Equal(t, "CLAIMED", claim.Status)
	_, err = s.Claim(ctx, ev, b, visB, now)
	assert.ErrorIs(t, err, entity.ErrTurnMissed, "tenggat klaim sudah lewat")

	// Sweeper: the expired turn is found, marked missed, and its constraint keys are cleaned so the buyer can rejoin.
	expired, err := s.ExpiredActive(ctx, ev, now)
	require.NoError(t, err)
	assert.Contains(t, expired, b)
	cleaned, err := s.MarkMissedAndClean(ctx, ev, b, now)
	require.NoError(t, err)
	assert.True(t, cleaned)
	_, found, err = s.FindByVisitor(ctx, ev, visB)
	require.NoError(t, err)
	assert.False(t, found, "visitor bisa antre lagi")
	missedAgain, err := s.MarkMissedAndClean(ctx, ev, "tidak-ada", now)
	require.NoError(t, err)
	assert.False(t, missedAgain)

	// Leaving frees the slot of an admitted entry; leaving something unknown is not found.
	wasActive, err := s.Leave(ctx, ev, a, visA, now)
	require.NoError(t, err)
	assert.True(t, wasActive)
	_, err = s.Leave(ctx, ev, "tidak-ada", "visitor-x", now)
	assert.ErrorIs(t, err, entity.ErrNotFound)

	// End a promoted entry with the outcome Booking reports (idempotent).
	_, err = s.Promote(ctx, ev, []outbound.PromoteCandidate{{EntryID: c, AllocationID: "alloc-c", ClaimDeadlineMs: now + 60_000}}, 5, now)
	require.NoError(t, err)
	final, err := s.EndEntry(ctx, ev, c, "PAID", now)
	require.NoError(t, err)
	assert.NotEmpty(t, final)
	_, err = s.EndEntry(ctx, ev, c, "PAID", now)
	require.NoError(t, err, "idempoten")
	_, _, _ = visC, nikC, c
}

func TestQueueStore_SaleStatusDocLeaseAndNotifications(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	ev := "evt-misc"

	// Sale state and the cached status document.
	require.NoError(t, s.SetSale(ctx, ev, "OPEN", false))
	sale, err := s.GetSale(ctx, ev)
	require.NoError(t, err)
	assert.Equal(t, "OPEN", sale.SaleState)
	assert.False(t, sale.SoldOut)
	require.NoError(t, s.SetSale(ctx, ev, "CLOSED", true))
	sale, err = s.GetSale(ctx, ev)
	require.NoError(t, err)
	assert.True(t, sale.SoldOut)

	_, ok, err := s.GetStatusDoc(ctx, ev)
	require.NoError(t, err)
	assert.False(t, ok)
	require.NoError(t, s.SetStatusDoc(ctx, ev, []byte(`{"x":1}`), time.Minute))
	doc, ok, err := s.GetStatusDoc(ctx, ev)
	require.NoError(t, err)
	require.True(t, ok)
	assert.JSONEq(t, `{"x":1}`, string(doc))

	// A buyer whose quota is spent cannot join again.
	nik := fmt.Sprintf("%064x", 999)
	require.NoError(t, s.AddQuotaExhausted(ctx, ev, nik))
	res, err := s.Join(ctx, outbound.JoinParams{EventID: ev, EntryID: "e-q", VisitorID: "v-q", NikHash: nik, EmailHashHex: fmt.Sprintf("%064x", 998), NowMs: time.Now().UnixMilli()})
	require.NoError(t, err)
	assert.Equal(t, outbound.JoinQuotaExhausted, res.Outcome)

	// "Your turn is soon" is sent once per entry.
	was, err := s.WasNotifiedSoon(ctx, ev, "e1")
	require.NoError(t, err)
	assert.False(t, was)
	require.NoError(t, s.MarkNotifiedSoon(ctx, ev, "e1"))
	was, err = s.WasNotifiedSoon(ctx, ev, "e1")
	require.NoError(t, err)
	assert.True(t, was)

	// Only one worker instance promotes an event at a time.
	got, err := s.AcquirePromoterLease(ctx, ev, 5_000)
	require.NoError(t, err)
	assert.True(t, got)
	got, err = s.AcquirePromoterLease(ctx, ev, 5_000)
	require.NoError(t, err)
	assert.False(t, got, "lease dipegang instance lain")
	require.NoError(t, s.ReleasePromoterLease(ctx, ev))
	got, err = s.AcquirePromoterLease(ctx, ev, 5_000)
	require.NoError(t, err)
	assert.True(t, got)
	require.NoError(t, s.ReleasePromoterLease(ctx, ev))
}

func TestRateLimiter_AllowsUpToTheLimitPerWindow(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	rl := NewRateLimiter(s.c, s.keyer, time.Now)
	key := fmt.Sprintf("it:%d", time.Now().UnixNano())
	for i := 1; i <= 3; i++ {
		ok, err := rl.Allow(ctx, key, 3, time.Minute)
		require.NoError(t, err)
		assert.True(t, ok, "permintaan ke-%d", i)
	}
	ok, err := rl.Allow(ctx, key, 3, time.Minute)
	require.NoError(t, err)
	assert.False(t, ok, "di atas batas")
	other, err := rl.Allow(ctx, key+"-lain", 3, time.Minute)
	require.NoError(t, err)
	assert.True(t, other, "kunci lain tidak terpengaruh")
}
