//go:build integration

package valkey

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

const t0 = int64(1_900_000_000_000) // fixed virtual epoch (ms)

func joinAt(t *testing.T, s *QueueStore, ev string, p outbound.JoinParams, nowMs int64) outbound.JoinResult {
	t.Helper()
	p.EventID, p.NowMs, p.OneActivePerVisitor = ev, nowMs, true
	if p.TokenTTL == 0 {
		p.TokenTTL = time.Hour
	}
	res, err := s.Join(context.Background(), p)
	require.NoError(t, err)
	return res
}

func lease(t *testing.T, s *QueueStore, ev string) string {
	t.Helper()
	tok, ok, err := s.AcquirePromoterLease(context.Background(), ev, 5*time.Second)
	require.NoError(t, err)
	require.True(t, ok)
	t.Cleanup(func() { _ = s.ReleasePromoterLease(context.Background(), ev, tok) })
	return tok
}

func status(t *testing.T, s *QueueStore, ev, id string) string {
	t.Helper()
	e, ok, err := s.GetEntry(context.Background(), ev, id)
	require.NoError(t, err)
	require.True(t, ok)
	return string(e.Status)
}

// R01.4 / R02.2 / R03.5: the token generation lives server-side; rotation (re-attach, reuse after ENDED) revokes
// earlier tokens; a valid token of an ENDED entry is NOT_FOUND, never INVALID.
func TestTokenGen_JoinRotationAndHeartbeatStates(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	ev := "ev-gen"

	a := joinAt(t, s, ev, outbound.JoinParams{EntryID: "a", NewVisitorID: "v-a", AccountID: "acc"}, t0)
	require.Equal(t, outbound.JoinCreated, a.Outcome)
	assert.EqualValues(t, 1, a.Gen)
	assert.EqualValues(t, 1, a.Seq, "first join: seq 1 (strict FIFO by seq)")

	same := joinAt(t, s, ev, outbound.JoinParams{EntryID: "x", NewVisitorID: "v-x", TokenVisitorID: "v-a", TokenGen: 1, AccountID: "acc"}, t0+1)
	assert.Equal(t, outbound.JoinExisting, same.Outcome)
	assert.EqualValues(t, 1, same.Gen, "valid token + same entry never rotates")

	hb, err := s.Heartbeat(ctx, ev, "v-a", 1, t0+2)
	require.NoError(t, err)
	assert.Equal(t, outbound.HeartbeatOK, hb.Tag)

	// device 2 re-attaches with the bearer and no token: generation rotates, T1 is revoked
	re := joinAt(t, s, ev, outbound.JoinParams{EntryID: "y", NewVisitorID: "v-y", AccountID: "acc"}, t0+3)
	assert.Equal(t, outbound.JoinReattach, re.Outcome)
	assert.EqualValues(t, 2, re.Gen)
	hb, err = s.Heartbeat(ctx, ev, "v-a", 1, t0+4)
	require.NoError(t, err)
	assert.Equal(t, outbound.HeartbeatInvalid, hb.Tag, "rotated-out token -> 401")
	hb, err = s.Heartbeat(ctx, ev, "v-a", 2, t0+4)
	require.NoError(t, err)
	assert.Equal(t, outbound.HeartbeatOK, hb.Tag)

	// a stale generation on join is ignored (treated as no token): the bearer re-attaches again
	stale := joinAt(t, s, ev, outbound.JoinParams{EntryID: "z", NewVisitorID: "v-z", TokenVisitorID: "v-a", TokenGen: 1, AccountID: "acc"}, t0+5)
	assert.Equal(t, outbound.JoinReattach, stale.Outcome)
	assert.EqualValues(t, 3, stale.Gen)

	// the entry ends; the still-valid token answers NOT_FOUND and the entry stays ENDED
	_, err = s.EndEntry(ctx, ev, "a", "SWEEP_MISSED", t0+6)
	require.NoError(t, err)
	_, err = s.Leave(ctx, ev, "a", "v-a", t0+6)
	require.NoError(t, err)
	hb, err = s.Heartbeat(ctx, ev, "v-a", 3, t0+7)
	require.NoError(t, err)
	assert.Equal(t, outbound.HeartbeatNotFound, hb.Tag)
	last, ok, err := s.FindLastByVisitor(ctx, ev, "v-a")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "LEFT", string(last.Status))
	assert.Equal(t, "LEFT", last.EndReason)

	// rejoin with the valid token of the ENDED entry: new entry, same visitor id, rotated token
	rj := joinAt(t, s, ev, outbound.JoinParams{EntryID: "b", NewVisitorID: "v-b", TokenVisitorID: "v-a", TokenGen: 3}, t0+8)
	assert.Equal(t, outbound.JoinCreated, rj.Outcome)
	assert.Equal(t, "v-a", rj.VisitorID)
	assert.EqualValues(t, 4, rj.Gen)
	g, ok, err := s.TokenGen(ctx, ev, "v-a")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.EqualValues(t, 4, g)
}

// S2 (R02.3): 100 untokened joins with one bearer: one entry and exactly ONE valid generation.
func TestTokenGen_S2ExactlyOneValidToken(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	gens := make([]int64, 100)
	for i := range gens {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := joinAt(t, s, "ev-s2g", outbound.JoinParams{EntryID: fmt.Sprintf("e%d", i), NewVisitorID: fmt.Sprintf("nv%d", i), AccountID: "acc"}, t0)
			gens[i] = r.Gen
		}()
	}
	wg.Wait()
	cur, _, err := s.TokenGen(ctx, "ev-s2g", gensVisitor(t, s, "ev-s2g", "acc"))
	require.NoError(t, err)
	valid := 0
	for _, g := range gens {
		if g == cur {
			valid++
		}
	}
	assert.Equal(t, 1, valid, "only the last rotation is valid")
	assert.EqualValues(t, 100, cur, "1 create + 99 rotations")
}

func gensVisitor(t *testing.T, s *QueueStore, ev, acct string) string {
	t.Helper()
	id, err := s.c.Do(context.Background(), s.c.B().Get().Key(s.kAccount(ev, acct)).Build()).ToString()
	require.NoError(t, err)
	e, _, err := s.GetEntry(context.Background(), ev, id)
	require.NoError(t, err)
	return e.VisitorID
}

// TC-ANON-03: STALE strictly after stale_after, revive at the original seq (+1 position for those behind),
// ENDED strictly after end_after, never for an admitted entry.
func TestLiveness_StaleReviveEnd(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	ev := "ev-live"
	staleAfter, endAfter := int64(90_000), int64(300_000)
	for i, id := range []string{"a", "b", "c", "d"} {
		joinAt(t, s, ev, outbound.JoinParams{EntryID: id, NewVisitorID: "v-" + id}, t0+int64(i))
	}
	tok := lease(t, s, ev)
	sweep := func(now int64) outbound.LivenessResult {
		r, err := s.SweepLiveness(ctx, ev, tok, now-staleAfter, now-endAfter, 100, now)
		require.NoError(t, err)
		require.False(t, r.LeaseLost)
		return r
	}

	// c and d keep heartbeating; a and b go silent
	for _, now := range []int64{t0 + 30_000, t0 + 60_000, t0 + 89_000} {
		for _, v := range []string{"v-c", "v-d"} {
			_, err := s.Heartbeat(ctx, ev, v, 1, now)
			require.NoError(t, err)
		}
	}
	assert.Zero(t, sweep(t0+90_000).Staled, "exactly at the threshold: still WAITING")
	r := sweep(t0 + 90_002)
	assert.Equal(t, 2, r.Staled)
	assert.Equal(t, "STALE", status(t, s, ev, "a"))
	assert.Equal(t, "STALE", status(t, s, ev, "b"))
	top, err := s.TopWaiting(ctx, ev, 10)
	require.NoError(t, err)
	assert.Equal(t, []string{"c", "d"}, top, "STALE entries are skipped by the promoter")
	pos, err := s.LivePosition(ctx, ev, 4)
	require.NoError(t, err)
	assert.EqualValues(t, 2, pos, "d is 2nd live")

	// revive b: same seq, d's displayed position +1, nobody else moves
	hb, err := s.Heartbeat(ctx, ev, "v-b", 1, t0+95_000)
	require.NoError(t, err)
	assert.True(t, hb.Revived)
	assert.EqualValues(t, 2, hb.Seq)
	assert.Equal(t, "WAITING", status(t, s, ev, "b"))
	pos, _ = s.LivePosition(ctx, ev, 4)
	assert.EqualValues(t, 3, pos)
	pos, _ = s.LivePosition(ctx, ev, 3)
	assert.EqualValues(t, 2, pos)

	// admit c, then everyone goes silent: only the never-admitted silent a ends; admitted c never ends by silence
	_, err = s.Promote(ctx, ev, []outbound.PromoteCandidate{{EntryID: "c", AllocationID: "al-c", ClaimDeadlineMs: t0 + 10_000_000}}, 10, t0+96_000, outbound.PromoteOpts{LeaseToken: tok})
	require.NoError(t, err)
	assert.Zero(t, sweep(t0+300_000).Ended, "a at exactly end_after: not ended")
	r = sweep(t0 + 300_002)
	assert.Equal(t, 1, r.Ended)
	assert.Equal(t, "ENDED", status(t, s, ev, "a"))
	ea, _, _ := s.GetEntry(ctx, ev, "a")
	assert.Equal(t, outbound.EndStaleTimeout, ea.EndReason)
	r = sweep(t0 + 10_000_000)
	assert.Equal(t, "ADMITTED", status(t, s, ev, "c"), "silence never ends an admitted entry")
	hb, err = s.Heartbeat(ctx, ev, "v-a", 1, t0+10_000_001)
	require.NoError(t, err)
	assert.Equal(t, outbound.HeartbeatNotFound, hb.Tag)
}

func TestLiveness_FencedByLease(t *testing.T) {
	s := newTestStore(t)
	joinAt(t, s, "ev-fence", outbound.JoinParams{EntryID: "a", NewVisitorID: "v-a"}, t0)
	r, err := s.SweepLiveness(context.Background(), "ev-fence", "not-the-lease", t0+1_000_000, t0, 10, t0+1_000_000)
	require.NoError(t, err)
	assert.True(t, r.LeaseLost)
	assert.Equal(t, "WAITING", status(t, s, "ev-fence", "a"))
}

// TC-ANON-04: a revive racing the promoter and the liveness sweeper (all under the real scripts) never yields a double
// admission and never admits an entry while it is STALE. ≥ 1,000 runs.
func TestRevivePromoteRace(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	var doubles, staleAdmits atomic.Int64
	for run := 0; run < 1000; run++ {
		ev := fmt.Sprintf("ev-race-%d", run)
		joinAt(t, s, ev, outbound.JoinParams{EntryID: "e", NewVisitorID: "v"}, t0)
		var wg sync.WaitGroup
		wg.Add(3)
		go func() { // revive / keep-alive
			defer wg.Done()
			_, _ = s.Heartbeat(ctx, ev, "v", 1, t0+100_000)
		}()
		go func() { // liveness sweeper under the lease
			defer wg.Done()
			if tok, ok, _ := s.AcquirePromoterLease(ctx, ev, time.Second); ok {
				_, _ = s.SweepLiveness(ctx, ev, tok, t0+50_000, t0-1, 10, t0+100_000)
				_ = s.ReleasePromoterLease(ctx, ev, tok)
			}
		}()
		go func() { // promoter under the lease: candidate read and promote in one lease
			defer wg.Done()
			for i := 0; i < 2; i++ {
				if tok, ok, _ := s.AcquirePromoterLease(ctx, ev, time.Second); ok {
					top, _ := s.TopWaiting(ctx, ev, 1)
					if len(top) == 1 {
						e, _, _ := s.GetEntry(ctx, ev, "e")
						res, _ := s.Promote(ctx, ev, []outbound.PromoteCandidate{{EntryID: "e", AllocationID: "al", ClaimDeadlineMs: t0 + 1_000_000}}, 5, t0+100_000, outbound.PromoteOpts{LeaseToken: tok})
						if len(res.Promoted) == 1 && e.Status != "WAITING" {
							staleAdmits.Add(1)
						}
					}
					_ = s.ReleasePromoterLease(ctx, ev, tok)
				}
			}
		}()
		wg.Wait()
		n, _ := s.ActiveCount(ctx, ev)
		if n > 1 {
			doubles.Add(1)
		}
		st := status(t, s, ev, "e")
		require.Contains(t, []string{"ADMITTED", "WAITING", "STALE"}, st)
		if st == "STALE" {
			top, _ := s.TopWaiting(ctx, ev, 1)
			require.Empty(t, top)
		}
	}
	assert.Zero(t, doubles.Load(), "never admitted twice")
	assert.Zero(t, staleAdmits.Load(), "never admitted while STALE")
}

// TC-QUE: WaitingSeqAt returns the seq of the live entry at a 0-based position; strict FIFO by seq.
func TestSeqs_WaitingSeqAtAndCount(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	ev := "ev-seqs"
	for i := 1; i <= 5; i++ {
		joinAt(t, s, ev, outbound.JoinParams{EntryID: fmt.Sprintf("e%d", i), NewVisitorID: fmt.Sprintf("v%d", i)}, t0)
	}
	n, err := s.WaitingCount(ctx, ev)
	require.NoError(t, err)
	assert.EqualValues(t, 5, n)
	r, ok, err := s.WaitingSeqAt(ctx, ev, 2)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.EqualValues(t, 3, r)
	_, ok, err = s.WaitingSeqAt(ctx, ev, 9)
	require.NoError(t, err)
	assert.False(t, ok)
	seqs := []int64{}
	for i := int64(0); i < 5; i++ {
		v, _, _ := s.WaitingSeqAt(ctx, ev, i)
		seqs = append(seqs, v)
	}
	assert.True(t, sort.SliceIsSorted(seqs, func(i, j int) bool { return seqs[i] < seqs[j] }))
}
