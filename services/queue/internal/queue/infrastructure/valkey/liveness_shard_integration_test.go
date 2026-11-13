//go:build integration

package valkey

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/queueshard"
	pkgvalkey "github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/valkey"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// hbClient opens a Valkey client on TEST_VALKEY_SERVER with a unique key prefix. The caller owns Close.
func hbClient(t *testing.T) *pkgvalkey.Client {
	t.Helper()
	server := os.Getenv("TEST_VALKEY_SERVER")
	if server == "" {
		t.Fatal("TEST_VALKEY_SERVER wajib diset untuk test integrasi heartbeat")
	}
	prefix := fmt.Sprintf("hbs%d", time.Now().UnixNano()%1_000_000_000)
	c, err := pkgvalkey.New(context.Background(), pkgvalkey.Config{Server: server, PrefixEnv: prefix})
	require.NoError(t, err)
	return c
}

// TestHeartbeat_UsesStoredShard: an entry joined under 4 shards keeps its liveness keys in the shard it was stored
// under, even after a redeploy changes the shard count to 8. The heartbeat must address the stored shard, not the
// shard the new count would compute for the id.
func TestHeartbeat_UsesStoredShard(t *testing.T) {
	ctx := context.Background()
	c := hbClient(t)
	t.Cleanup(c.Close)
	joined := NewQueueStore(c.VK(), c, clock.System{}, 4)
	redeployed := NewQueueStore(c.VK(), c, clock.System{}, 8)
	ev := "ev-hb-stored-shard"

	// An id whose shard differs between 4 and 8 shards, so the two computations cannot agree by accident.
	id := ""
	for i := 0; id == ""; i++ {
		if cand := fmt.Sprintf("hb-entry-%d", i); queueshard.Shard(cand, 4) != queueshard.Shard(cand, 8) {
			id = cand
		}
	}
	j := joinAt(t, joined, ev, outbound.JoinParams{EntryID: id, NewVisitorID: "v-hb"}, t0)
	stored := joined.shardOf(id)

	hb, err := redeployed.Heartbeat(ctx, ev, "v-hb", j.Gen, t0+5_000)
	require.NoError(t, err)
	require.Equal(t, outbound.HeartbeatOK, hb.Tag)

	score, ok := zscore(t, redeployed, redeployed.kHbShard(ev, stored), id)
	require.True(t, ok, "the entry stays in its stored shard hb:%s", stored)
	assert.Equal(t, float64(t0+5_000), score, "the stored shard's hb:%s gets the new score", stored)
	for sh := 0; sh < 16; sh++ {
		if strconv.Itoa(sh) == stored {
			continue
		}
		_, ok := zscore(t, redeployed, redeployed.kHbShard(ev, strconv.Itoa(sh)), id)
		assert.False(t, ok, "no other hb:%d gains the id", sh)
	}
}

// TestHeartbeat_VlastReadErrorIsReturned: a failed vlast read is an error, not a silent fall-through to the Lua step.
func TestHeartbeat_VlastReadErrorIsReturned(t *testing.T) {
	c := hbClient(t)
	s := NewQueueStore(c.VK(), c, clock.System{}, 4)
	c.Close()

	hb, err := s.Heartbeat(context.Background(), "ev-hb-closed", "v-closed", 1, t0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "valkey: get vlast")
	assert.NotEqual(t, outbound.HeartbeatOK, hb.Tag)
}

// hbScore reads the entry's score from its stored shard's hb key (nil => absent). It addresses the shard the entry
// actually lives in (shardOf), the same way every production mutation does, so a stranded entry in a different shard
// is NOT masked by this check.
func hbScore(t *testing.T, s *QueueStore, ev, id string) (float64, bool) {
	t.Helper()
	key := s.kHbShard(ev, s.shardOf(id))
	return zscore(t, s, key, id)
}

func hbStaleScore(t *testing.T, s *QueueStore, ev, id string) (float64, bool) {
	t.Helper()
	key := s.kHbStaleShard(ev, s.shardOf(id))
	return zscore(t, s, key, id)
}

// TC-ANON-05b (ADR-030 Amendment, design-change §4.5 finding-9): sharding the hb/hbstale liveness index must not
// strand entries. Every mutation that leaves the liveness set — admit (promote), leave, end — must remove the entry
// from the SHARD IT LIVES IN, and a following sweep must not re-process a terminal entry. Verified at B=16 (sharded)
// and B=1 (parity with the pre-sharding single key).
func TestLivenessShard_NoStrandedEntriesOnAdmitLeaveEnd(t *testing.T) {
	for _, b := range []int{1, 16} {
		b := b
		t.Run(shardName(b), func(t *testing.T) {
			s := newTestStoreShards(t, b)
			ctx := context.Background()
			ev := "ev-shard"

			// Seed four entries across shards; with b=16 and distinct ids they land in different shards.
			ids := []string{"a", "b", "c", "d"}
			for i, id := range ids {
				joinAt(t, s, ev, outbound.JoinParams{EntryID: id, NewVisitorID: "v-" + id}, t0+int64(i))
				// After join the entry is live in its shard's hb set.
				_, ok := hbScore(t, s, ev, id)
				assert.True(t, ok, "join seeds hb:{s} for %s (shard %s)", id, s.shardOf(id))
			}

			tok := lease(t, s, ev)

			// (a) admit a: promote must ZREM from a's shard; hb:{s} no longer holds a.
			_, err := s.Promote(ctx, ev, []outbound.PromoteCandidate{{EntryID: "a", AllocationID: "al-a", ClaimDeadlineMs: t0 + 10_000_000}}, 10, t0+1_000, outbound.PromoteOpts{LeaseToken: tok})
			require.NoError(t, err)
			_, ok := hbScore(t, s, ev, "a")
			assert.False(t, ok, "admitted entry a is removed from its hb shard (never re-swept)")

			// (b) leave b: cleanup must ZREM from b's hb and hbstale shard keys.
			_, err = s.Leave(ctx, ev, "b", "v-b", t0+2_000)
			require.NoError(t, err)
			_, ok = hbScore(t, s, ev, "b")
			assert.False(t, ok, "left entry b is removed from its hb shard")
			_, ok = hbStaleScore(t, s, ev, "b")
			assert.False(t, ok, "left entry b is removed from its hbstale shard")

			// (c) end c: ALREADY_ACTIVE is the promoter outcome that ends a WAITING entry (class B); its dropSets must
			// ZREM from c's hb and hbstale shard keys. (SWEEP_MISSED only ends ADMITTED past-deadline entries, never a
			// WAITING one — it returns IGNORED for WAITING, so it is not a way to leave the liveness set.)
			_, err = s.EndEntry(ctx, ev, "c", "ALREADY_ACTIVE", t0+3_000)
			require.NoError(t, err)
			_, ok = hbScore(t, s, ev, "c")
			assert.False(t, ok, "ended entry c is removed from its hb shard")
			_, ok = hbStaleScore(t, s, ev, "c")
			assert.False(t, ok, "ended entry c is removed from its hbstale shard")

			// A following sweep must not re-process the terminal entries (counts do not drift): only d remains live,
			// and it is not silent yet, so nothing staled/ended.
			r, err := s.SweepLiveness(ctx, ev, tok, t0+4_000-90_000, t0+4_000-300_000, 1000, t0+4_000)
			require.NoError(t, err)
			require.False(t, r.LeaseLost)
			assert.Zero(t, r.Staled, "no live entry is silent yet")
			assert.Zero(t, r.Ended, "terminal entries are not re-processed by the sweep")
		})
	}
}

// TC-ANON-03b: STALE → revive → end must behave identically at B=1 and B=16 — the entry moves hb:{s} ⇄ hbstale:{s}
// within its own shard and revives at its original seq position (the ordering sets are global, so seq is preserved).
func TestLivenessShard_StaleReviveEndParity(t *testing.T) {
	for _, b := range []int{1, 16} {
		b := b
		t.Run(shardName(b), func(t *testing.T) {
			s := newTestStoreShards(t, b)
			ctx := context.Background()
			ev := "ev-shard-live"
			staleAfter, endAfter := int64(90_000), int64(300_000)
			for i, id := range []string{"a", "b"} {
				joinAt(t, s, ev, outbound.JoinParams{EntryID: id, NewVisitorID: "v-" + id}, t0+int64(i))
			}
			tok := lease(t, s, ev)

			// a goes silent, b keeps alive; sweep marks a STALE -> a leaves hb:{s}, enters hbstale:{s}.
			_, err := s.Heartbeat(ctx, ev, "v-b", 1, t0+89_000)
			require.NoError(t, err)
			r, err := s.SweepLiveness(ctx, ev, tok, t0+90_002-staleAfter, t0+90_002-endAfter, 100, t0+90_002)
			require.NoError(t, err)
			assert.Equal(t, 1, r.Staled)
			assert.Equal(t, "STALE", status(t, s, ev, "a"))
			_, ok := hbScore(t, s, ev, "a")
			assert.False(t, ok, "STALE a left its hb shard")
			_, ok = hbStaleScore(t, s, ev, "a")
			assert.True(t, ok, "STALE a is in its hbstale shard")

			// revive a: back to WAITING at its original seq, hb:{s} holds it again, hbstale:{s} does not.
			hb, err := s.Heartbeat(ctx, ev, "v-a", 1, t0+95_000)
			require.NoError(t, err)
			assert.True(t, hb.Revived)
			assert.EqualValues(t, 1, hb.Seq, "revive restores original seq position")
			assert.Equal(t, "WAITING", status(t, s, ev, "a"))
			_, ok = hbScore(t, s, ev, "a")
			assert.True(t, ok, "revived a is back in its hb shard")
			_, ok = hbStaleScore(t, s, ev, "a")
			assert.False(t, ok, "revived a left its hbstale shard")
			top, err := s.TopWaiting(ctx, ev, 10)
			require.NoError(t, err)
			assert.Equal(t, []string{"a", "b"}, top, "original FIFO order by seq preserved after revive")

			// a goes silent again and is never revived; b stays alive so only a is swept. After end_after a ENDS (stale
			// timeout), leaving both shards. b is kept alive before each sweep so the counts isolate a.
			_, err = s.Heartbeat(ctx, ev, "v-b", 1, t0+300_000)
			require.NoError(t, err)
			r, err = s.SweepLiveness(ctx, ev, tok, t0+300_000-staleAfter, t0+300_000-endAfter, 100, t0+300_000)
			require.NoError(t, err)
			assert.Equal(t, 1, r.Staled, "only a staled again (b is alive)")
			assert.Zero(t, r.Ended, "a not yet past end_after")
			_, ok = hbStaleScore(t, s, ev, "a")
			assert.True(t, ok, "a is in its hbstale shard after staling again")

			// a's hbstale score is its last signal (t0+95_000). End when endBefore passes it: now-endAfter > 95_000.
			_, err = s.Heartbeat(ctx, ev, "v-b", 1, t0+400_000)
			require.NoError(t, err)
			r, err = s.SweepLiveness(ctx, ev, tok, t0+400_000-staleAfter, t0+400_000-endAfter, 100, t0+400_000)
			require.NoError(t, err)
			assert.Equal(t, 1, r.Ended, "a ended by stale timeout")
			assert.Equal(t, "ENDED", status(t, s, ev, "a"))
			_, ok = hbScore(t, s, ev, "a")
			assert.False(t, ok, "ended a is in no hb shard")
			_, ok = hbStaleScore(t, s, ev, "a")
			assert.False(t, ok, "ended a is in no hbstale shard")
		})
	}
}

func shardName(b int) string {
	if b == 1 {
		return "B=1_single_key_parity"
	}
	return "B=16_sharded"
}
