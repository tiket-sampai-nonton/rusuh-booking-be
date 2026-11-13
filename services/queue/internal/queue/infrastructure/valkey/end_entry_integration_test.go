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
)

// joinAdmit joins entry n and admits it with the given deadline.
func joinAdmit(t *testing.T, s *QueueStore, ev string, n int, deadlineMs, now int64) (id, vis, nik string) {
	t.Helper()
	id, vis, nik = joinOne(t, s, ev, n, now)
	res := promoteOne(t, s, ev, id, fmt.Sprintf("alloc-%d", n), deadlineMs, now, 100)
	require.Contains(t, res.Promoted, id)
	return
}

func constraintKeysGone(t *testing.T, s *QueueStore, ev, nik, vis string, n int) {
	t.Helper()
	assert.False(t, keyExists(t, s, s.kAccount(ev, nik)), "kunci NIK harus bersih")
	assert.False(t, keyExists(t, s, s.kVisitor(ev, vis)), "kunci visitor harus bersih")
}

// TC-QUE-21 / P4: SWEEP_MISSED only ever ends an ADMITTED entry whose deadline has passed.
func TestEndEntry_SweepMissed_OnlyExpiredAdmittedBecomesMissed(t *testing.T) {
	s := mustStore(t)
	ctx := context.Background()
	ev := evID(t)
	now := time.Now().UnixMilli()

	exp, visExp, nikExp := joinAdmit(t, s, ev, 1, now-1_000, now)
	live, _, _ := joinAdmit(t, s, ev, 2, now+60_000, now)
	wait, _, _ := joinOne(t, s, ev, 3, now)

	r, err := s.EndEntry(ctx, ev, exp, "SWEEP_MISSED", now)
	require.NoError(t, err)
	assert.Equal(t, "OK", r.Tag)
	assert.Equal(t, "MISSED", r.Status)
	assert.True(t, r.Changed())
	assert.Equal(t, "MISSED", hget(t, s, s.kEntry(ev, exp), "status"))
	_, inActive := zscore(t, s, s.kActive(ev), exp)
	assert.False(t, inActive, "slot dibebaskan")
	constraintKeysGone(t, s, ev, nikExp, visExp, 1)

	r, err = s.EndEntry(ctx, ev, live, "SWEEP_MISSED", now)
	require.NoError(t, err)
	assert.Equal(t, "IGNORED", r.Tag)
	assert.Equal(t, "ADMITTED", hget(t, s, s.kEntry(ev, live), "status"), "giliran yang masih hidup tidak disentuh")
	_, inActive = zscore(t, s, s.kActive(ev), live)
	assert.True(t, inActive)

	r, err = s.EndEntry(ctx, ev, wait, "SWEEP_MISSED", now)
	require.NoError(t, err)
	assert.Equal(t, "IGNORED", r.Tag)
	assert.Equal(t, "WAITING", hget(t, s, s.kEntry(ev, wait), "status"))
	_, inWaiting := zscore(t, s, s.kWaiting(ev), wait)
	assert.True(t, inWaiting, "entry WAITING tetap antre")

	again, err := s.EndEntry(ctx, ev, exp, "SWEEP_MISSED", now)
	require.NoError(t, err)
	assert.False(t, again.Changed(), "panggilan kedua no-op")
}

// TC-QUE-21 / P5: a CLAIMED entry whose deadline score is stale is re-scored out of the expiry scan and never MISSED.
func TestEndEntry_SweepMissed_LegacyClaimedIsRescoredNotMissed(t *testing.T) {
	s := mustStore(t)
	ctx := context.Background()
	ev := evID(t)
	now := time.Now().UnixMilli()

	id, _, _ := joinAdmit(t, s, ev, 1, now-1_000, now)
	// Legacy shape: claim.lua of the base version left a CLAIMED entry scored by its (past) deadline.
	hsetRaw(t, s, s.kEntry(ev, id), "status", "CLAIMED", "claimedAtMs", fmt.Sprint(now-2_000))

	r, err := s.EndEntry(ctx, ev, id, "SWEEP_MISSED", now)
	require.NoError(t, err)
	assert.Equal(t, "IGNORED", r.Tag)
	assert.Equal(t, "CLAIMED", r.Status)
	assert.Equal(t, "CLAIMED", hget(t, s, s.kEntry(ev, id), "status"))
	score, ok := zscore(t, s, s.kActive(ev), id)
	require.True(t, ok)
	assert.True(t, math.IsInf(score, 1), "skor di-+inf-kan supaya keluar dari scan kedaluwarsa, got %v", score)
	expired, err := s.ExpiredActive(ctx, ev, now+10_000, 100)
	require.NoError(t, err)
	assert.NotContains(t, expired, id)
}

// F5/P12: an orphan id (hash gone) is removed from both sets so a sweep page of orphans cannot pin later entries.
func TestEndEntry_OrphanIDIsRemovedFromActiveAndWaiting(t *testing.T) {
	s := mustStore(t)
	ctx := context.Background()
	ev := evID(t)
	now := time.Now().UnixMilli()
	for _, outcome := range []string{"SWEEP_MISSED", "PROMOTER_ORPHAN"} {
		orphan := "orphan-" + outcome
		zaddRaw(t, s, s.kActive(ev), float64(now-5_000), orphan)
		zaddRaw(t, s, s.kWaiting(ev), 1, orphan)
		r, err := s.EndEntry(ctx, ev, orphan, outcome, now)
		require.NoError(t, err)
		assert.Equal(t, "IGNORED", r.Tag)
		assert.Equal(t, "NO_ENTRY", r.Status)
		_, inA := zscore(t, s, s.kActive(ev), orphan)
		_, inW := zscore(t, s, s.kWaiting(ev), orphan)
		assert.False(t, inA, outcome)
		assert.False(t, inW, outcome)
	}
}

// Booking outcomes (class C): UNCLAIMED on ADMITTED/CLAIMED frees the slot (MISSED); PAID on WAITING is ignored.
func TestEndEntry_BookingOutcomes(t *testing.T) {
	s := mustStore(t)
	ctx := context.Background()
	ev := evID(t)
	now := time.Now().UnixMilli()

	adm, visA, nikA := joinAdmit(t, s, ev, 1, now+60_000, now)
	r, err := s.EndEntry(ctx, ev, adm, "UNCLAIMED", now)
	require.NoError(t, err)
	assert.Equal(t, "MISSED", r.Status, "Booking UNCLAIMED pada ADMITTED => MISSED")
	_, inA := zscore(t, s, s.kActive(ev), adm)
	assert.False(t, inA)
	constraintKeysGone(t, s, ev, nikA, visA, 1)

	clm, _, _ := joinAdmit(t, s, ev, 2, now+60_000, now)
	hsetRaw(t, s, s.kEntry(ev, clm), "status", "CLAIMED")
	r, err = s.EndEntry(ctx, ev, clm, "UNCLAIMED", now)
	require.NoError(t, err)
	assert.Equal(t, "MISSED", r.Status, "Booking UNCLAIMED pada CLAIMED (tak pernah aktif) membebaskan slot")
	_, inA = zscore(t, s, s.kActive(ev), clm)
	assert.False(t, inA)

	paid, _, _ := joinAdmit(t, s, ev, 3, now+60_000, now)
	r, err = s.EndEntry(ctx, ev, paid, "PAID", now)
	require.NoError(t, err)
	assert.Equal(t, "COMPLETED", r.Status)

	wait, _, _ := joinOne(t, s, ev, 4, now)
	r, err = s.EndEntry(ctx, ev, wait, "PAID", now)
	require.NoError(t, err)
	assert.Equal(t, "IGNORED", r.Tag, "PAID pada WAITING idempoten diabaikan")
	assert.Equal(t, "WAITING", hget(t, s, s.kEntry(ev, wait), "status"))
	_, inW := zscore(t, s, s.kWaiting(ev), wait)
	assert.True(t, inW)
}

// Class B (promoter outcomes) only end WAITING entries and remove them from waiting (Q-01: no head-of-line block).
func TestEndEntry_PromoterOutcomesEndWaitingOnly(t *testing.T) {
	s := mustStore(t)
	ctx := context.Background()
	ev := evID(t)
	now := time.Now().UnixMilli()

	for i, outcome := range []string{"ALREADY_ACTIVE"} { // ADR-030 removed BUYER_QUOTA_EXHAUSTED and UNREADABLE_PII
		n := 10 + i
		id, vis, nik := joinOne(t, s, ev, n, now)
		r, err := s.EndEntry(ctx, ev, id, outcome, now)
		require.NoError(t, err)
		assert.Equal(t, "OK", r.Tag, outcome)
		assert.Equal(t, "ENDED", r.Status, outcome)
		_, inW := zscore(t, s, s.kWaiting(ev), id)
		assert.False(t, inW, "%s: entry yang diakhiri tidak boleh menyumbat head", outcome)
		constraintKeysGone(t, s, ev, nik, vis, n)

		adm, _, _ := joinAdmit(t, s, ev, 20+i, now+60_000, now)
		r, err = s.EndEntry(ctx, ev, adm, outcome, now)
		require.NoError(t, err)
		assert.Equal(t, "IGNORED", r.Tag, "%s pada ADMITTED diabaikan", outcome)
		assert.Equal(t, "ADMITTED", hget(t, s, s.kEntry(ev, adm), "status"))
	}
}

// TC-QUE-22: a MISSED cleanup that arrives after the buyer rejoined must not delete the newer entry's keys.
func TestEndEntry_MissedCleanupKeepsNewerConstraintKeys(t *testing.T) {
	s := mustStore(t)
	ctx := context.Background()
	ev := evID(t)
	now := time.Now().UnixMilli()

	old, _, nik := joinAdmit(t, s, ev, 1, now-1_000, now)
	hsetRaw(t, s, s.kEntry(ev, old), "status", "MISSED") // claim.lua marked it after the deadline
	// Rejoin with the same NIK: simulate by pointing the NIK key at a newer entry.
	require.NoError(t, s.c.Do(ctx, s.c.B().Set().Key(s.kAccount(ev, nik)).Value("entry-newer").Build()).Error())

	r, err := s.EndEntry(ctx, ev, old, "SWEEP_MISSED", now)
	require.NoError(t, err)
	assert.Equal(t, "OK_CLEANUP", r.Tag)
	_, inA := zscore(t, s, s.kActive(ev), old)
	assert.False(t, inA, "MISSED harus melepas slot di active")
	got, err := s.c.Do(ctx, s.c.B().Get().Key(s.kAccount(ev, nik)).Build()).ToString()
	require.NoError(t, err)
	assert.Equal(t, "entry-newer", got, "kunci milik entry baru tidak boleh terhapus")

	r, err = s.EndEntry(ctx, ev, old, "SWEEP_MISSED", now)
	require.NoError(t, err)
	got, _ = s.c.Do(ctx, s.c.B().Get().Key(s.kAccount(ev, nik)).Build()).ToString()
	assert.Equal(t, "entry-newer", got, "diproses dua kali tetap aman")
	_ = r
}

// C6.5 (P2-15): a CLAIMED slot is bounded by the claimMaxMs of its claim. Past that bound SWEEP_MISSED ends it as
// ENDED/CLAIM_TIMEOUT and frees the slot; a later Booking outcome is ignored because the entry is already terminal.
func TestEndEntry_ClaimedPastBoundIsEnded(t *testing.T) {
	s := mustStore(t)
	ctx := context.Background()
	ev := evID(t)
	t0 := time.Now().UnixMilli()

	id, vis, nik := joinAdmit(t, s, ev, 1, t0+60_000, t0)
	_, err := s.Claim(ctx, ev, id, vis, t0, 1000)
	require.NoError(t, err)

	expired, err := s.ExpiredActive(ctx, ev, t0+999, 100)
	require.NoError(t, err)
	assert.NotContains(t, expired, id, "belum melewati batas klaim")
	expired, err = s.ExpiredActive(ctx, ev, t0+1000, 100)
	require.NoError(t, err)
	assert.Contains(t, expired, id, "tepat di batas klaim sudah kedaluwarsa")

	r, err := s.EndEntry(ctx, ev, id, "SWEEP_MISSED", t0+1000)
	require.NoError(t, err)
	assert.Equal(t, "OK", r.Tag)
	assert.Equal(t, "ENDED", r.Status)
	assert.Equal(t, "ENDED", hget(t, s, s.kEntry(ev, id), "status"))
	assert.Equal(t, "CLAIM_TIMEOUT", hget(t, s, s.kEntry(ev, id), "endReason"))
	assert.Equal(t, int64(0), zcard(t, s, s.kActive(ev)), "slot dibebaskan")
	constraintKeysGone(t, s, ev, nik, vis, 1)

	r, err = s.EndEntry(ctx, ev, id, "PAID", t0+2000)
	require.NoError(t, err)
	assert.Equal(t, "IGNORED", r.Tag, "outcome Booking terlambat diabaikan")
	assert.Equal(t, "ENDED", hget(t, s, s.kEntry(ev, id), "status"))
}

// D-C6.6 (P2-16): a Booking UNCLAIMED for a WAITING entry is ignored. Its reservation lapsed before Queue admitted it, so
// the entry keeps its place in the queue and Booking can grant it again; it is never ended or MISSED here.
func TestEndEntry_UnclaimedForWaitingIsIgnored(t *testing.T) {
	s := mustStore(t)
	ctx := context.Background()
	ev := evID(t)
	now := time.Now().UnixMilli()

	wait, vis, nik := joinOne(t, s, ev, 1, now)
	r, err := s.EndEntry(ctx, ev, wait, "UNCLAIMED", now)
	require.NoError(t, err)
	assert.Equal(t, "IGNORED", r.Tag)
	assert.Equal(t, "WAITING", r.Status)
	assert.Equal(t, "WAITING", hget(t, s, s.kEntry(ev, wait), "status"))
	_, inW := zscore(t, s, s.kWaiting(ev), wait)
	assert.True(t, inW, "entry WAITING tetap antre")
	assert.True(t, keyExists(t, s, s.kAccount(ev, nik)), "constraint keys tetap ada")
	assert.True(t, keyExists(t, s, s.kVisitor(ev, vis)), "constraint keys tetap ada")
}

// REQ-QSH-11.2: ExpiredActive is bounded, and a limit of zero is a programming error.
func TestExpiredActive_RespectsLimit(t *testing.T) {
	s := mustStore(t)
	ctx := context.Background()
	ev := evID(t)
	now := time.Now().UnixMilli()
	for i := 0; i < 10; i++ {
		zaddRaw(t, s, s.kActive(ev), float64(now-int64(100-i)), fmt.Sprintf("m%02d", i))
	}
	ids, err := s.ExpiredActive(ctx, ev, now, 4)
	require.NoError(t, err)
	assert.Len(t, ids, 4)
	assert.Equal(t, []string{"m00", "m01", "m02", "m03"}, ids, "yang paling lama kedaluwarsa dulu")
}

func TestExpiredActive_LimitZeroIsError(t *testing.T) {
	s := mustStore(t)
	_, err := s.ExpiredActive(context.Background(), evID(t), time.Now().UnixMilli(), 0)
	require.Error(t, err)
}

// P12: a page of orphans larger than the limit must not block a later expired entry; each sweep round shrinks the page.
func TestSweep_OrphansBeyondLimitClear(t *testing.T) {
	s := mustStore(t)
	ctx := context.Background()
	ev := evID(t)
	now := time.Now().UnixMilli()
	const limit = 5
	for i := 0; i < limit+5; i++ {
		zaddRaw(t, s, s.kActive(ev), float64(now-int64(10_000-i)), fmt.Sprintf("orphan-%02d", i))
	}
	real, _, _ := joinAdmit(t, s, ev, 1, now-1, now) // expired, scored after every orphan
	rounds := (limit + 6 + limit - 1) / limit
	for r := 0; r < rounds; r++ {
		ids, err := s.ExpiredActive(ctx, ev, now, limit)
		require.NoError(t, err)
		for _, id := range ids {
			_, err := s.EndEntry(ctx, ev, id, "SWEEP_MISSED", now)
			require.NoError(t, err)
		}
	}
	assert.Equal(t, "MISSED", hget(t, s, s.kEntry(ev, real), "status"), "entry nyata tercapai dalam %d putaran", rounds)
	assert.Equal(t, int64(0), zcard(t, s, s.kActive(ev)))
}
