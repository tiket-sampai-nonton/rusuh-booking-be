//go:build integration

package valkey

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// TC-C8.2 (D-C8.2, P1-4): every terminal write gives the entry hash PEXPIRE entryRetention; a live entry has no TTL.
func TestTerminalEntriesGetRetention(t *testing.T) {
	s := mustStore(t)
	ctx := context.Background()
	now := time.Now().UnixMilli()
	retention := outbound.EntryRetention.Milliseconds()

	// retained asserts the entry hash now expires within the retention window (PTTL in (0, retention]).
	retained := func(t *testing.T, ev, id string) {
		t.Helper()
		p := pttl(t, s, s.kEntry(ev, id))
		assert.Greater(t, p, int64(0), "terminal entry must carry a TTL")
		assert.LessOrEqual(t, p, retention, "TTL is at most the retention")
	}
	// live asserts the entry hash has no TTL (PTTL -1), the state of every WAITING, ADMITTED and CLAIMED entry.
	live := func(t *testing.T, ev, id string) {
		t.Helper()
		assert.Equal(t, int64(-1), pttl(t, s, s.kEntry(ev, id)), "live entry must not expire")
	}

	t.Run("EndEntry PAID", func(t *testing.T) {
		ev := evID(t)
		id, _, _ := joinAdmit(t, s, ev, 1, now+60_000, now)
		r, err := s.EndEntry(ctx, ev, id, "PAID", now)
		require.NoError(t, err)
		require.True(t, r.Changed())
		retained(t, ev, id)
	})

	t.Run("Leave", func(t *testing.T) {
		ev := evID(t)
		id, vis, _ := joinOne(t, s, ev, 2, now)
		_, err := s.Leave(ctx, ev, id, vis, now)
		require.NoError(t, err)
		assert.Equal(t, "LEFT", hget(t, s, s.kEntry(ev, id), "status"))
		retained(t, ev, id)
	})

	t.Run("EndWaitingSoldOut", func(t *testing.T) {
		ev := evID(t)
		id, _, _ := joinOne(t, s, ev, 3, now)
		r, err := s.EndWaitingSoldOut(ctx, ev, now, 10)
		require.NoError(t, err)
		require.Equal(t, 1, r.Ended)
		assert.Equal(t, "SOLD_OUT", hget(t, s, s.kEntry(ev, id), "status"))
		retained(t, ev, id)
	})

	t.Run("liveness END", func(t *testing.T) {
		ev := evID(t)
		id, _, _ := joinOne(t, s, ev, 4, now)
		tok := lease(t, s, ev)
		staleAt := now + 90_002
		r, err := s.SweepLiveness(ctx, ev, tok, staleAt-90_000, staleAt-300_000, 100, staleAt)
		require.NoError(t, err)
		require.Equal(t, 1, r.Staled)
		endAt := now + 300_002
		r, err = s.SweepLiveness(ctx, ev, tok, endAt-90_000, endAt-300_000, 100, endAt)
		require.NoError(t, err)
		require.Equal(t, 1, r.Ended)
		assert.Equal(t, "ENDED", hget(t, s, s.kEntry(ev, id), "status"))
		retained(t, ev, id)
	})

	t.Run("claim MISSED", func(t *testing.T) {
		ev := evID(t)
		id, vis, _ := joinAdmit(t, s, ev, 5, now+1_000, now)
		_, err := s.Claim(ctx, ev, id, vis, now+2_000, testClaimMaxMs)
		require.ErrorIs(t, err, entity.ErrTurnMissed)
		assert.Equal(t, "MISSED", hget(t, s, s.kEntry(ev, id), "status"))
		retained(t, ev, id)
	})

	t.Run("CLAIMED has no TTL", func(t *testing.T) {
		ev := evID(t)
		id, vis, _ := joinAdmit(t, s, ev, 6, now+60_000, now)
		_, err := s.Claim(ctx, ev, id, vis, now, testClaimMaxMs)
		require.NoError(t, err)
		live(t, ev, id)
	})

	t.Run("WAITING has no TTL", func(t *testing.T) {
		ev := evID(t)
		id, _, _ := joinOne(t, s, ev, 7, now)
		live(t, ev, id)
	})
}

// TC-C8.2 (P3-13): RetireEvent gives every fixed key of a finished event a TTL, and a repeat call never moves a
// deadline forward (the status publisher calls it on every tick).
func TestRetireEvent_ExpiresFixedKeys(t *testing.T) {
	s := mustStore(t)
	ctx := context.Background()
	ev := evID(t)
	now := time.Now().UnixMilli()
	const ttl = time.Hour

	_, _, _ = joinAdmit(t, s, ev, 1, now+60_000, now) // ADMITTED: in active
	b, _, _ := joinOne(t, s, ev, 2, now)              // silent: STALE
	c, _, _ := joinOne(t, s, ev, 3, now+10_000)       // still WAITING
	tok := lease(t, s, ev)
	r, err := s.SweepLiveness(ctx, ev, tok, now+5, now-1_000_000, 100, now+5)
	require.NoError(t, err)
	require.Equal(t, 1, r.Staled)
	require.NoError(t, s.SetSale(ctx, ev, "OPEN", false))
	require.NoError(t, s.c.Do(ctx, s.c.B().Set().Key(s.kStatus(ev)).Value("{}").Build()).Error())

	fixed := []string{
		s.kSeq(ev), s.kWaiting(ev), s.k(ev, "stale"), s.kActive(ev),
		s.kHbShard(ev, s.shardOf(c)), s.kHbStaleShard(ev, s.shardOf(b)),
		s.kStatus(ev), s.kSale(ev),
	}
	for _, k := range fixed {
		require.True(t, keyExists(t, s, k), "fixture key %s must exist", k)
		require.Equal(t, int64(-1), pttl(t, s, k), "fixture key %s starts without a TTL", k)
	}
	// The held promoter lease already carries its own short TTL; RetireEvent must leave that deadline alone.
	lockBefore := pttl(t, s, s.kLease(ev))
	require.Greater(t, lockBefore, int64(0), "the held lease has its own short TTL")
	fixed = append(fixed, s.kLease(ev))

	require.NoError(t, s.RetireEvent(ctx, ev, ttl))
	first := map[string]int64{}
	for _, k := range fixed {
		p := pttl(t, s, k)
		first[k] = p
		assert.Greater(t, p, int64(0), "%s must expire", k)
		assert.LessOrEqual(t, p, ttl.Milliseconds(), "%s expires within the retention", k)
	}
	assert.LessOrEqual(t, pttl(t, s, s.kLease(ev)), lockBefore, "RetireEvent must not extend the lease's own TTL")

	require.NoError(t, s.RetireEvent(ctx, ev, ttl))
	for _, k := range fixed {
		assert.LessOrEqual(t, pttl(t, s, k), first[k], "a repeat call must not push %s's deadline forward", k)
	}
}
