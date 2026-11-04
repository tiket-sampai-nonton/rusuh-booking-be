//go:build integration

package valkey

import (
	"context"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// legacyWaiting writes an entry exactly as the base (pre-QSH) join.lua did: no unreadableN field, binary emailEnc.
func legacyWaiting(t *testing.T, s *QueueStore, ev string, n int, seq int64, now int64) (id, vis, nik string) {
	t.Helper()
	id, vis, nik = fmt.Sprintf("legacy-%d", n), fmt.Sprintf("lv-%d", n), fmt.Sprintf("%064x", 5000+n)
	hsetRaw(t, s, s.kEntry(ev, id),
		"seq", fmt.Sprint(seq), "visitorId", vis, "buyerNikHash", nik, "emailEnc", "\xff\xfe\x00bin\x80",
		"emailHash", fmt.Sprintf("%064x", 6000+n), "accountId", "", "nikMasked", "3174********0001",
		"status", "WAITING", "joinedAtMs", fmt.Sprint(now))
	zaddRaw(t, s, s.kWaiting(ev), float64(seq), id)
	require.NoError(t, s.c.Do(context.Background(), s.c.B().Set().Key(s.kNik(ev, nik)).Value(id).Build()).Error())
	require.NoError(t, s.c.Do(context.Background(), s.c.B().Set().Key(s.kVisitor(ev, vis)).Value(id).Build()).Error())
	return
}

// legacyActive writes an ADMITTED entry the way the base promote.lua did: scored by its deadline.
func legacyActive(t *testing.T, s *QueueStore, ev string, n int, status string, deadline, now int64) (id, vis, nik string) {
	t.Helper()
	id, vis, nik = legacyWaiting(t, s, ev, n, int64(n), now)
	require.NoError(t, s.c.Do(context.Background(), s.c.B().Zrem().Key(s.kWaiting(ev)).Member(id).Build()).Error())
	hsetRaw(t, s, s.kEntry(ev, id), "status", status, "admittedAtMs", fmt.Sprint(now), "allocationId", "alloc-"+id, "claimDeadlineMs", fmt.Sprint(deadline))
	zaddRaw(t, s, s.kActive(ev), float64(deadline), id)
	return
}

// REQ-QSH-11.5: data written by the BASE scripts must keep working through the NEW scripts.
func TestLegacy(t *testing.T) {
	t.Run("a_claimed_rescored", func(t *testing.T) {
		s := mustStore(t)
		ctx := context.Background()
		ev := evID(t)
		now := time.Now().UnixMilli()
		id, _, _ := legacyActive(t, s, ev, 1, "CLAIMED", now-5_000, now) // base claim.lua left it scored by deadline
		r, err := s.EndEntry(ctx, ev, id, outbound.EndSweepMissed, now)
		require.NoError(t, err)
		assert.Equal(t, "IGNORED", r.Tag)
		score, ok := zscore(t, s, s.kActive(ev), id)
		require.True(t, ok)
		assert.True(t, math.IsInf(score, 1), "CLAIMED lama harus di-+inf-kan, got %v", score)
		assert.Equal(t, "CLAIMED", hget(t, s, s.kEntry(ev, id), "status"))
	})

	t.Run("b_mixed_page_clears", func(t *testing.T) {
		s := mustStore(t)
		ctx := context.Background()
		ev := evID(t)
		now := time.Now().UnixMilli()
		const limit = 4
		// A page larger than the sweep limit made of orphans, legacy CLAIMED and stale terminal members.
		// More orphans than the page limit: if the script did not remove an orphan from active, the oldest `limit`
		// members would be orphans forever and the genuinely expired entry behind them could never be reached.
		for i := 0; i < limit+1; i++ {
			zaddRaw(t, s, s.kActive(ev), float64(now-100_000+int64(i)), fmt.Sprintf("orphan-%d", i))
		}
		for i := 0; i < 3; i++ {
			legacyActive(t, s, ev, 10+i, "CLAIMED", now-90_000+int64(i), now)
		}
		for i := 0; i < 2; i++ {
			id, _, _ := legacyActive(t, s, ev, 20+i, "ENDED", now-80_000+int64(i), now)
			_ = id
		}
		real, _, _ := legacyActive(t, s, ev, 30, "ADMITTED", now-1, now) // genuinely expired, scored after every poison member
		for round := 0; round < 8; round++ {
			ids, err := s.ExpiredActive(ctx, ev, now, limit)
			require.NoError(t, err)
			for _, id := range ids {
				_, err := s.EndEntry(ctx, ev, id, outbound.EndSweepMissed, now)
				require.NoError(t, err)
			}
		}
		assert.Equal(t, "MISSED", hget(t, s, s.kEntry(ev, real), "status"), "entry kedaluwarsa tercapai walau halaman penuh member beracun")
	})

	t.Run("c_admitted_expires_missed", func(t *testing.T) {
		s := mustStore(t)
		ctx := context.Background()
		ev := evID(t)
		now := time.Now().UnixMilli()
		id, vis, nik := legacyActive(t, s, ev, 1, "ADMITTED", now-1_000, now)
		r, err := s.EndEntry(ctx, ev, id, outbound.EndSweepMissed, now)
		require.NoError(t, err)
		assert.Equal(t, "MISSED", r.Status)
		_, inActive := zscore(t, s, s.kActive(ev), id)
		assert.False(t, inActive)
		assert.False(t, keyExists(t, s, s.kNik(ev, nik)))
		assert.False(t, keyExists(t, s, s.kVisitor(ev, vis)))
	})

	t.Run("d_waiting_promote_leave", func(t *testing.T) {
		s := mustStore(t)
		ctx := context.Background()
		ev := evID(t)
		now := time.Now().UnixMilli()
		promoted, _, _ := legacyWaiting(t, s, ev, 1, 1, now)
		leaver, leaverVis, _ := legacyWaiting(t, s, ev, 2, 2, now)

		res := promoteOne(t, s, ev, promoted, "alloc-legacy", now+60_000, now, 5)
		require.Equal(t, []string{promoted}, res.Promoted)
		assert.Equal(t, "alloc-legacy", hget(t, s, s.kEntry(ev, promoted), "allocationId"))
		_, inW := zscore(t, s, s.kWaiting(ev), promoted)
		assert.False(t, inW, "entry yang dipromosikan keluar dari waiting")

		lr, err := s.Leave(ctx, ev, leaver, leaverVis, now)
		require.NoError(t, err)
		assert.Equal(t, "OK", lr.Tag)
		assert.Equal(t, "LEFT", hget(t, s, s.kEntry(ev, leaver), "status"))
		_, inW = zscore(t, s, s.kWaiting(ev), leaver)
		assert.False(t, inW, "entry yang keluar tidak boleh tersisa di waiting")
	})

	t.Run("e_emailenc_roundtrip", func(t *testing.T) {
		s := mustStore(t)
		ctx := context.Background()
		ev := evID(t)
		now := time.Now().UnixMilli()
		id, _, _ := legacyWaiting(t, s, ev, 1, 1, now)
		ent, found, err := s.GetEntry(ctx, ev, id)
		require.NoError(t, err)
		require.True(t, found)
		assert.Equal(t, []byte("\xff\xfe\x00bin\x80"), ent.EmailEnc, "emailEnc biner harus utuh byte demi byte")
	})
}
