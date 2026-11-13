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

	t.Run("SweepLivenessTick END", func(t *testing.T) {
		ev := evID(t)
		id, _, _ := joinOne(t, s, ev, 8, now)
		staleAt := now + 90_002
		_, _, err := s.SweepLivenessTick(ctx, ev, staleAt-90_000, staleAt-300_000, 100, staleAt, 5*time.Second, 0, 0)
		require.NoError(t, err)
		endAt := now + 300_002
		r, _, err := s.SweepLivenessTick(ctx, ev, endAt-90_000, endAt-300_000, 100, endAt, 5*time.Second, 0, 0)
		require.NoError(t, err)
		require.Equal(t, 1, r.Ended)
		assert.Equal(t, "ENDED", hget(t, s, s.kEntry(ev, id), "status"))
		retained(t, ev, id)
	})

	t.Run("EndEntry SWEEP_MISSED", func(t *testing.T) {
		ev := evID(t)
		id, _, _ := joinAdmit(t, s, ev, 9, now-1_000, now)
		r, err := s.EndEntry(ctx, ev, id, "SWEEP_MISSED", now)
		require.NoError(t, err)
		require.True(t, r.Changed())
		assert.Equal(t, "MISSED", hget(t, s, s.kEntry(ev, id), "status"))
		retained(t, ev, id)
	})

	t.Run("EndEntry ALREADY_ACTIVE", func(t *testing.T) {
		ev := evID(t)
		id, _, _ := joinOne(t, s, ev, 10, now)
		r, err := s.EndEntry(ctx, ev, id, "ALREADY_ACTIVE", now)
		require.NoError(t, err)
		require.True(t, r.Changed())
		assert.Equal(t, "ENDED", hget(t, s, s.kEntry(ev, id), "status"))
		retained(t, ev, id)
	})

	t.Run("EndWaitingSoldOut two entries", func(t *testing.T) {
		ev := evID(t)
		a, _, _ := joinOne(t, s, ev, 11, now)
		b, _, _ := joinOne(t, s, ev, 12, now+1)
		r, err := s.EndWaitingSoldOut(ctx, ev, now, 10)
		require.NoError(t, err)
		require.Equal(t, 2, r.Ended)
		retained(t, ev, a)
		retained(t, ev, b)
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

// TC-C8.2 (P3-13): RetireEvent gives the fixed keys of a drained, finished event a TTL, and a repeat call never moves a
// deadline forward (the status publisher calls it on every tick). A vtok key is never part of it (D-C6.1).
func TestRetireEvent_ExpiresFixedKeys(t *testing.T) {
	s := mustStore(t)
	ctx := context.Background()
	ev := evID(t)
	now := time.Now().UnixMilli()
	const ttl = time.Hour

	id, vis, _ := joinAdmit(t, s, ev, 1, now+60_000, now)
	_, err := s.EndEntry(ctx, ev, id, "PAID", now) // nothing live is left in the event
	require.NoError(t, err)
	require.NoError(t, s.SetSale(ctx, ev, "OPEN", false))
	_, err = s.AdvanceNotifyWatermark(ctx, ev, 1) // the pre-turn watermark exists once notify has run (C8.3)
	require.NoError(t, err)
	require.NoError(t, s.c.Do(ctx, s.c.B().Set().Key(s.kStatus(ev)).Value("{}").Build()).Error())
	// A token record without a TTL: RetireEvent must leave it alone (vtok is not in the fixed list).
	require.NoError(t, s.c.Do(ctx, s.c.B().Set().Key(s.k(ev, "vtok:"+vis)).Value("1").Build()).Error())

	fixed := []string{s.kSeq(ev), s.kStatus(ev), s.kSale(ev), s.kNotifySeq(ev)}
	for _, k := range fixed {
		require.True(t, keyExists(t, s, k), "fixture key %s must exist", k)
		require.Equal(t, int64(-1), pttl(t, s, k), "fixture key %s starts without a TTL", k)
	}
	// The held promoter lease already carries its own short TTL; RetireEvent must leave that deadline alone.
	require.NoError(t, s.c.Do(ctx, s.c.B().Set().Key(s.kLease(ev)).Value("held").Px(5*time.Second).Build()).Error())
	lockBefore := pttl(t, s, s.kLease(ev))
	require.Greater(t, lockBefore, int64(0))
	fixed = append(fixed, s.kLease(ev))

	ok, err := s.RetireEvent(ctx, ev, ttl)
	require.NoError(t, err)
	require.True(t, ok, "a drained event is retired")
	first := map[string]int64{}
	for _, k := range fixed {
		p := pttl(t, s, k)
		first[k] = p
		assert.Greater(t, p, int64(0), "%s must expire", k)
		assert.LessOrEqual(t, p, ttl.Milliseconds(), "%s expires within the retention", k)
	}
	assert.LessOrEqual(t, first[s.kLease(ev)], lockBefore, "RetireEvent must not extend the lease's own TTL")
	assert.Equal(t, int64(-1), pttl(t, s, s.k(ev, "vtok:"+vis)), "vtok is never retired")

	ok, err = s.RetireEvent(ctx, ev, ttl)
	require.NoError(t, err)
	require.True(t, ok)
	for _, k := range fixed {
		assert.LessOrEqual(t, pttl(t, s, k), first[k], "a repeat call must not push %s's deadline forward", k)
	}
}

// TC-C8.2 (P3-13, D-C8.2): retirement is one atomic step. A WAITING entry present when the script runs blocks it, so
// neither waiting nor seq (nor the hb shard) gets a TTL while the entry is live.
func TestRetireEvent_WaitingEntryBlocksAtomically(t *testing.T) {
	s := mustStore(t)
	ctx := context.Background()
	ev := evID(t)
	now := time.Now().UnixMilli()

	id, _, _ := joinOne(t, s, ev, 1, now)
	ok, err := s.RetireEvent(ctx, ev, time.Hour)
	require.NoError(t, err)
	assert.False(t, ok, "a WAITING entry blocks retirement")

	assert.Equal(t, int64(-1), pttl(t, s, s.kWaiting(ev)), "waiting of a live entry must not expire")
	assert.Equal(t, int64(-1), pttl(t, s, s.kSeq(ev)), "seq of a live entry must not expire")
	assert.Equal(t, int64(-1), pttl(t, s, s.kHbShard(ev, s.shardOf(id))), "hb shard of a live entry must not expire")
	assert.Equal(t, "WAITING", hget(t, s, s.kEntry(ev, id), "status"))
}

// TC-C8.2 (P3-13): a join after a finished event was retired must not leave the live entry under the old seq TTL.
func TestRetireEvent_JoinAfterRetireLeavesSeqPersistent(t *testing.T) {
	s := mustStore(t)
	ctx := context.Background()
	ev := evID(t)
	now := time.Now().UnixMilli()

	done, _, _ := joinAdmit(t, s, ev, 1, now+60_000, now)
	_, err := s.EndEntry(ctx, ev, done, "PAID", now)
	require.NoError(t, err)
	_, err = s.AdvanceNotifyWatermark(ctx, ev, 1)
	require.NoError(t, err)
	ok, err := s.RetireEvent(ctx, ev, time.Hour)
	require.NoError(t, err)
	require.True(t, ok)
	require.Greater(t, pttl(t, s, s.kSeq(ev)), int64(0), "retired event: seq carries the TTL")
	require.Greater(t, pttl(t, s, s.kNotifySeq(ev)), int64(0), "retired event: the notify watermark carries the TTL")

	live, _, _ := joinOne(t, s, ev, 2, now+1)
	assert.Equal(t, int64(-1), pttl(t, s, s.kSeq(ev)), "a live join must persist seq")
	assert.Equal(t, int64(-1), pttl(t, s, s.kNotifySeq(ev)), "a live join must persist the notify watermark, or it expires under the live entry and re-notifies it")
	assert.Equal(t, int64(-1), pttl(t, s, s.kWaiting(ev)), "waiting of the live entry must not expire")
	assert.Equal(t, int64(-1), pttl(t, s, s.kEntry(ev, live)), "the live entry must not expire")
}

// TC-C8.3 fix round 1 (I-3): advancing the notify watermark keeps the TTL that RetireEvent gave it. A plain SET would clear
// the TTL, so a retired event's watermark would never expire. The value still moves forward as before.
func TestNotifyWatermark_AdvanceKeepsRetirementTTL(t *testing.T) {
	s := mustStore(t)
	ctx := context.Background()
	ev := evID(t)
	const ttl = time.Hour

	_, err := s.AdvanceNotifyWatermark(ctx, ev, 1) // the watermark exists before the event is retired
	require.NoError(t, err)
	ok, err := s.RetireEvent(ctx, ev, ttl) // no entries: the event is drained
	require.NoError(t, err)
	require.True(t, ok, "a drained event is retired")
	require.Greater(t, pttl(t, s, s.kNotifySeq(ev)), int64(0), "retired event: the watermark carries the TTL")

	prev, err := s.AdvanceNotifyWatermark(ctx, ev, 5)
	require.NoError(t, err)
	assert.Equal(t, int64(1), prev, "the advance still reads the previous watermark")
	assert.Greater(t, pttl(t, s, s.kNotifySeq(ev)), int64(0), "advancing must keep the retirement TTL, not make the watermark immortal")
	assert.LessOrEqual(t, pttl(t, s, s.kNotifySeq(ev)), ttl.Milliseconds(), "the watermark still expires within the retention")
	assert.Equal(t, int64(5), mustWatermark(t, s, ev), "the stored value moved forward to 5")
}

// mustWatermark reads the stored notify watermark of the event (0 when absent).
func mustWatermark(t *testing.T, s *QueueStore, ev string) int64 {
	t.Helper()
	v, err := s.c.Do(context.Background(), s.c.B().Get().Key(s.kNotifySeq(ev)).Build()).AsInt64()
	require.NoError(t, err)
	return v
}
