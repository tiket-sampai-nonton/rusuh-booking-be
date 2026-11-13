//go:build integration

package valkey

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// joinOne joins an anonymous entry bound to account "acct-<n>" (so both constraint keys exist) and returns
// (entryID, visitorID, accountID). Anonymous join has no NIK/email (ADR-030).
func joinOne(t *testing.T, s *QueueStore, ev string, n int, nowMs int64) (entryID, visitorID, accountID string) {
	t.Helper()
	entryID, visitorID, accountID = fmt.Sprintf("entry-%d", n), fmt.Sprintf("visitor-%d", n), fmt.Sprintf("acct-%d", n)
	res, err := s.Join(context.Background(), outbound.JoinParams{
		EventID: ev, EntryID: entryID, NewVisitorID: visitorID, AccountID: accountID, NowMs: nowMs, OneActivePerVisitor: true,
	})
	require.NoError(t, err)
	require.Equal(t, outbound.JoinCreated, res.Outcome)
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
	promoted := promoteBatch(t, s, ev, []outbound.PromoteCandidate{
		{EntryID: a, AllocationID: "alloc-a", ClaimDeadlineMs: now + 60_000},
		{EntryID: b, AllocationID: "alloc-b", ClaimDeadlineMs: now - 1_000}, // already past: will be missed
	}, 2, now)
	assert.ElementsMatch(t, []string{a, b}, promoted.Promoted)
	full := promoteBatch(t, s, ev, []outbound.PromoteCandidate{{EntryID: c, AllocationID: "alloc-c", ClaimDeadlineMs: now + 60_000}}, 2, now)
	assert.Empty(t, full.Promoted, "slot penuh")
	assert.True(t, full.Full)

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
	_, err = s.Claim(ctx, ev, a, "visitor-lain", now, testClaimMaxMs)
	assert.ErrorIs(t, err, entity.ErrNotFound)
	claim, err := s.Claim(ctx, ev, a, visA, now, testClaimMaxMs)
	require.NoError(t, err)
	assert.Equal(t, "CLAIMED", claim.Status)
	_, err = s.Claim(ctx, ev, b, visB, now, testClaimMaxMs)
	assert.ErrorIs(t, err, entity.ErrTurnMissed, "tenggat klaim sudah lewat")

	// Sweeper: the expired turn is found, marked missed, and its constraint keys are cleaned so the buyer can rejoin.
	expired, err := s.ExpiredActive(ctx, ev, now, 100)
	require.NoError(t, err)
	assert.Contains(t, expired, b)
	cleaned, err := s.EndEntry(ctx, ev, b, outbound.EndSweepMissed, now)
	require.NoError(t, err)
	assert.True(t, cleaned.Changed())
	_, found, err = s.FindByVisitor(ctx, ev, visB)
	require.NoError(t, err)
	assert.False(t, found, "visitor bisa antre lagi")
	missedAgain, err := s.EndEntry(ctx, ev, "tidak-ada", outbound.EndSweepMissed, now)
	require.NoError(t, err)
	assert.False(t, missedAgain.Changed())

	// A CLAIMED entry cannot leave (QSH-OQ-3, provisional): the slot is freed by journey.ended, not by leave.
	ignored, err := s.Leave(ctx, ev, a, visA, now)
	require.NoError(t, err)
	assert.Equal(t, "IGNORED", ignored.Tag)
	assert.Equal(t, "CLAIMED", ignored.PrevStatus)
	// Leaving frees the slot of an admitted entry; leaving something unknown is not found.
	d, visD, _ := joinOne(t, s, ev, 4, now)
	promoteOne(t, s, ev, d, "alloc-d", now+60_000, now, 5)
	left, err := s.Leave(ctx, ev, d, visD, now)
	require.NoError(t, err)
	assert.Equal(t, "OK", left.Tag)
	assert.Equal(t, "ADMITTED", left.PrevStatus)
	_, err = s.Leave(ctx, ev, "tidak-ada", "visitor-x", now)
	assert.ErrorIs(t, err, entity.ErrNotFound)

	// End a promoted entry with the outcome Booking reports (idempotent).
	promoteBatch(t, s, ev, []outbound.PromoteCandidate{{EntryID: c, AllocationID: "alloc-c", ClaimDeadlineMs: now + 60_000}}, 5, now)
	final, err := s.EndEntry(ctx, ev, c, "PAID", now)
	require.NoError(t, err)
	assert.NotEmpty(t, final.Status)
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

	// Only one worker instance promotes an event at a time.
	tok, got, err := s.AcquirePromoterLease(ctx, ev, 5*time.Second)
	require.NoError(t, err)
	assert.True(t, got)
	_, got, err = s.AcquirePromoterLease(ctx, ev, 5*time.Second)
	require.NoError(t, err)
	assert.False(t, got, "lease dipegang instance lain")
	require.NoError(t, s.ReleasePromoterLease(ctx, ev, tok))
	tok, got, err = s.AcquirePromoterLease(ctx, ev, 5*time.Second)
	require.NoError(t, err)
	assert.True(t, got)
	require.NoError(t, s.ReleasePromoterLease(ctx, ev, tok))
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

// TC-C8.3 (PF-P1-5, D-C8.3): the pre-turn notify watermark advances atomically. Two concurrent advances to 50 see the old
// value exactly once (one gets 0, the other 50), so no seq is handed out twice. A lower target never moves it back.
func TestNotifyWatermark_AdvancesOnce(t *testing.T) {
	s := mustStore(t)
	ctx := context.Background()
	ev := evID(t)

	var wg sync.WaitGroup
	prev := make([]int64, 2)
	errs := make([]error, 2)
	for i := range prev {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			prev[i], errs[i] = s.AdvanceNotifyWatermark(ctx, ev, 50)
		}(i)
	}
	wg.Wait()
	require.NoError(t, errs[0])
	require.NoError(t, errs[1])
	sort.Slice(prev, func(a, b int) bool { return prev[a] < prev[b] })
	assert.Equal(t, []int64{0, 50}, prev, "one advance sees 0 and the other sees 50")

	p, err := s.AdvanceNotifyWatermark(ctx, ev, 30)
	require.NoError(t, err)
	assert.Equal(t, int64(50), p, "a lower target does not move the watermark back")
	p, err = s.AdvanceNotifyWatermark(ctx, ev, 60)
	require.NoError(t, err)
	assert.Equal(t, int64(50), p)
	p, err = s.AdvanceNotifyWatermark(ctx, ev, 60)
	require.NoError(t, err)
	assert.Equal(t, int64(60), p, "a repeat to the same target sees that target")
}

// TC-C8.3: WaitingBetween returns the live waiting ids with afterSeq < seq <= upToSeq, FIFO by seq, at most limit of them.
func TestWaitingBetween_SeqWindowFIFO(t *testing.T) {
	s := mustStore(t)
	ctx := context.Background()
	ev := evID(t)
	now := time.Now().UnixMilli()
	var ids []string
	for n := 1; n <= 5; n++ {
		id, _, _ := joinOne(t, s, ev, n, now+int64(n))
		ids = append(ids, id) // seq n
	}

	got, err := s.WaitingBetween(ctx, ev, 1, 4, 10)
	require.NoError(t, err)
	assert.Equal(t, ids[1:4], got, "seqs 2..4 in FIFO order")
	got, err = s.WaitingBetween(ctx, ev, 0, 5, 2)
	require.NoError(t, err)
	assert.Equal(t, ids[:2], got, "the limit keeps the first ids by seq")
	got, err = s.WaitingBetween(ctx, ev, 5, 9, 10)
	require.NoError(t, err)
	assert.Empty(t, got, "nothing above the watermark")
}
