//go:build integration

package valkey

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// --- leave.lua (T03, REQ-QSH-06) ---

func TestLeave_WrongOwnerOrUnknownIsNotFoundAndChangesNothing(t *testing.T) {
	s := mustStore(t)
	ctx := context.Background()
	ev := evID(t)
	now := time.Now().UnixMilli()
	id, vis, nik := joinOne(t, s, ev, 1, now)

	_, err := s.Leave(ctx, ev, id, "visitor-orang-lain", now)
	assert.ErrorIs(t, err, entity.ErrNotFound)
	_, err = s.Leave(ctx, ev, "tidak-ada", vis, now)
	assert.ErrorIs(t, err, entity.ErrNotFound)

	assert.Equal(t, "WAITING", hget(t, s, s.kEntry(ev, id), "status"), "pemilik lain tidak boleh mengubah status")
	_, inW := zscore(t, s, s.kWaiting(ev), id)
	assert.True(t, inW)
	assert.True(t, keyExists(t, s, s.kAccount(ev, nik)), "kunci constraint milik pemilik tetap ada")
	assert.True(t, keyExists(t, s, s.kVisitor(ev, vis)))
}

func TestLeave_TerminalAndClaimedAreIgnoredWithStatusUnchanged(t *testing.T) {
	s := mustStore(t)
	ctx := context.Background()
	ev := evID(t)
	now := time.Now().UnixMilli()
	for i, st := range []string{"COMPLETED", "ENDED", "LEFT", "MISSED", "CLAIMED"} {
		id, vis, _ := joinOne(t, s, ev, 100+i, now)
		hsetRaw(t, s, s.kEntry(ev, id), "status", st)
		res, err := s.Leave(ctx, ev, id, vis, now)
		require.NoError(t, err, st)
		assert.Equal(t, "IGNORED", res.Tag, st)
		assert.Equal(t, st, res.PrevStatus, st)
		assert.Equal(t, st, hget(t, s, s.kEntry(ev, id), "status"), "status %s tidak boleh berubah", st)
	}
}

// TC-QUE-17: a WAITING leave removes the entry from waiting and frees the buyer to rejoin at the back.
func TestLeave_WaitingLeavesAndCanRejoinAtTheBack(t *testing.T) {
	s := mustStore(t)
	ctx := context.Background()
	ev := evID(t)
	now := time.Now().UnixMilli()
	first, visFirst, _ := joinOne(t, s, ev, 1, now)
	second, _, _ := joinOne(t, s, ev, 2, now)

	res, err := s.Leave(ctx, ev, first, visFirst, now)
	require.NoError(t, err)
	assert.Equal(t, "OK", res.Tag)
	assert.Equal(t, "WAITING", res.PrevStatus)
	assert.Empty(t, res.AllocationID)
	assert.Equal(t, "LEFT", hget(t, s, s.kEntry(ev, first), "status"))
	top, err := s.TopWaiting(ctx, ev, 10)
	require.NoError(t, err)
	assert.Equal(t, []string{second}, top)

	rejoined, _, _ := joinOne(t, s, ev, 1, now) // same NIK/visitor/email constraint keys must be free again
	_ = rejoined
}

func TestLeave_AdmittedReturnsPreviousStatusAndAllocationAndFreesSlot(t *testing.T) {
	s := mustStore(t)
	ctx := context.Background()
	ev := evID(t)
	now := time.Now().UnixMilli()
	id, vis, nik := joinAdmit(t, s, ev, 1, now+60_000, now)

	res, err := s.Leave(ctx, ev, id, vis, now)
	require.NoError(t, err)
	assert.Equal(t, "OK", res.Tag)
	assert.Equal(t, "ADMITTED", res.PrevStatus)
	assert.Equal(t, "alloc-1", res.AllocationID, "Go melepas alokasi ini di Booking setelah commit")
	_, inA := zscore(t, s, s.kActive(ev), id)
	assert.False(t, inA)
	constraintKeysGone(t, s, ev, nik, vis, 1)
}

func TestLeave_DoesNotDeleteNewerEntrysConstraintKeys(t *testing.T) {
	s := mustStore(t)
	ctx := context.Background()
	ev := evID(t)
	now := time.Now().UnixMilli()
	id, vis, nik := joinOne(t, s, ev, 1, now)
	require.NoError(t, s.c.Do(ctx, s.c.B().Set().Key(s.kAccount(ev, nik)).Value("entry-newer").Build()).Error())
	_, err := s.Leave(ctx, ev, id, vis, now)
	require.NoError(t, err)
	got, err := s.c.Do(ctx, s.c.B().Get().Key(s.kAccount(ev, nik)).Build()).ToString()
	require.NoError(t, err)
	assert.Equal(t, "entry-newer", got)
}

// --- claim.lua (T04, REQ-QSH-07/08/11) ---

func TestClaim_StatusSpecificErrors(t *testing.T) {
	s := mustStore(t)
	ctx := context.Background()
	ev := evID(t)
	now := time.Now().UnixMilli()

	wait, visW, _ := joinOne(t, s, ev, 1, now)
	_, err := s.Claim(ctx, ev, wait, visW, now)
	assert.ErrorIs(t, err, entity.ErrNotAdmitted, "WAITING belum punya giliran")

	for i, st := range []string{"LEFT", "ENDED", "COMPLETED"} {
		id, vis, _ := joinOne(t, s, ev, 10+i, now)
		hsetRaw(t, s, s.kEntry(ev, id), "status", st)
		_, err := s.Claim(ctx, ev, id, vis, now)
		assert.ErrorIs(t, err, entity.ErrNotFound, st)
	}

	missed, visM, _ := joinOne(t, s, ev, 20, now)
	hsetRaw(t, s, s.kEntry(ev, missed), "status", "MISSED")
	_, err = s.Claim(ctx, ev, missed, visM, now)
	assert.ErrorIs(t, err, entity.ErrTurnMissed)

	exp, visE, _ := joinAdmit(t, s, ev, 30, now-1, now)
	_, err = s.Claim(ctx, ev, exp, visE, now)
	assert.ErrorIs(t, err, entity.ErrTurnMissed)
	assert.Equal(t, "MISSED", hget(t, s, s.kEntry(ev, exp), "status"))
	_, still := zscore(t, s, s.kActive(ev), exp)
	assert.True(t, still, "MISSED tetap di active sampai sweeper membersihkan")
}

// A wrong visitor must be indistinguishable from an unknown id (no entry-existence oracle).
func TestClaim_WrongVisitorIsAnsweredLikeUnknown(t *testing.T) {
	s := mustStore(t)
	ctx := context.Background()
	ev := evID(t)
	now := time.Now().UnixMilli()
	id, _, _ := joinAdmit(t, s, ev, 1, now+60_000, now)
	_, errWrong := s.Claim(ctx, ev, id, "visitor-lain", now)
	_, errUnknown := s.Claim(ctx, ev, "tidak-ada", "visitor-lain", now)
	require.ErrorIs(t, errWrong, entity.ErrNotFound)
	require.ErrorIs(t, errUnknown, entity.ErrNotFound)
	assert.Equal(t, errWrong.Error(), errUnknown.Error())
}

func TestClaim_LiveAdmittedReturnsAllocationAndLeavesTheExpiryScan(t *testing.T) {
	s := mustStore(t)
	ctx := context.Background()
	ev := evID(t)
	now := time.Now().UnixMilli()
	id, vis, _ := joinAdmit(t, s, ev, 1, now+1_000, now)

	res, err := s.Claim(ctx, ev, id, vis, now)
	require.NoError(t, err)
	assert.Equal(t, "CLAIMED", res.Status)
	assert.Equal(t, "alloc-1", res.AllocationID, "allocationId datang dari script, bukan dari GetEntry kedua")
	assert.Equal(t, "CLAIMED", hget(t, s, s.kEntry(ev, id), "status"))
	score, ok := zscore(t, s, s.kActive(ev), id)
	require.True(t, ok, "slot CLAIMED tetap dihitung di active")
	assert.True(t, math.IsInf(score, 1))

	expired, err := s.ExpiredActive(ctx, ev, now+3_600_000, 100)
	require.NoError(t, err)
	assert.NotContains(t, expired, id, "CLAIMED tidak boleh pernah dikembalikan scan kedaluwarsa (INV-3/4)")

	again, err := s.Claim(ctx, ev, id, vis, now)
	require.NoError(t, err)
	assert.Equal(t, "ALREADY", again.Status)
	assert.Equal(t, "alloc-1", again.AllocationID)
}

// TC-QUE-21: claim racing the sweeper must never end as MISSED with a valid token.
func TestClaimVsSweep_ClaimedEntryIsNeverMissed(t *testing.T) {
	s := mustStore(t)
	ctx := context.Background()
	ev := evID(t)
	now := time.Now().UnixMilli()
	id, vis, _ := joinAdmit(t, s, ev, 1, now+500, now)
	_, err := s.Claim(ctx, ev, id, vis, now)
	require.NoError(t, err)
	r, err := s.EndEntry(ctx, ev, id, "SWEEP_MISSED", now+10_000) // long after the original deadline
	require.NoError(t, err)
	assert.Equal(t, "IGNORED", r.Tag)
	assert.Equal(t, "CLAIMED", hget(t, s, s.kEntry(ev, id), "status"))
}

// TC-QUE-23b (P16, INV-1): a WAITING entry whose Booking grant is RESERVED but whose Promote was deferred (NOT_HEAD)
// leaves. The leave must clear every Valkey trace so the entry can never be a promoter candidate again, and it must
// report nothing to release (there is no allocation id in the hash: only Booking's own expiry backstop applies).
func TestLeaveWaitingReserved_ClearsEveryTraceAndReportsNothingToRelease(t *testing.T) {
	s := mustStore(t)
	ctx := context.Background()
	ev := evID(t)
	now := time.Now().UnixMilli()
	head, _, _ := joinOne(t, s, ev, 1, now)
	later, visLater, nikLater := joinOne(t, s, ev, 2, now)
	tok, ok, err := s.AcquirePromoterLease(ctx, ev, 5*time.Second)
	require.NoError(t, err)
	require.True(t, ok)
	defer func() { _ = s.ReleasePromoterLease(ctx, ev, tok) }()

	// Booking granted `later`, but it is not the head: promote.lua defers it (NOT_HEAD) and changes nothing.
	res, err := s.Promote(ctx, ev, []outbound.PromoteCandidate{{EntryID: later, AllocationID: "alloc-reserved", ClaimDeadlineMs: now + 60_000}}, 5, now, outbound.PromoteOpts{RequireHead: true, LeaseToken: tok})
	require.NoError(t, err)
	require.Empty(t, res.Promoted)
	require.Equal(t, "WAITING", hget(t, s, s.kEntry(ev, later), "status"))

	lr, err := s.Leave(ctx, ev, later, visLater, now)
	require.NoError(t, err)
	assert.Equal(t, "OK", lr.Tag)
	assert.Equal(t, "WAITING", lr.PrevStatus)
	assert.Empty(t, lr.AllocationID, "tidak ada alokasi di hash untuk dilepas")
	assert.Equal(t, "LEFT", hget(t, s, s.kEntry(ev, later), "status"))
	top, err := s.TopWaiting(ctx, ev, 10)
	require.NoError(t, err)
	assert.Equal(t, []string{head}, top, "entry yang keluar tidak lagi kandidat promoter")
	assert.Equal(t, int64(0), zcard(t, s, s.kActive(ev)))
	assert.False(t, keyExists(t, s, s.kAccount(ev, nikLater)), "kunci constraint bersih")
	assert.False(t, keyExists(t, s, s.kVisitor(ev, visLater)))
}

// SRS-F-10-02 / req 5.5: SOLD_OUT is terminal. An entry ended by booking.sold_out is not revived by a heartbeat, is not
// overwritten by a leave, answers claim with ErrSoldOut, and does not block a fresh join with the same visitor token.
func TestSoldOutEntryIsTerminal(t *testing.T) {
	s := mustStore(t)
	ctx := context.Background()
	ev := evID(t)
	now := time.Now().UnixMilli()
	id, vis, acct := joinOne(t, s, ev, 1, now)
	gen, ok, err := s.TokenGen(ctx, ev, vis)
	require.NoError(t, err)
	require.True(t, ok)

	ended, err := s.EndWaitingSoldOut(ctx, ev, now, 100)
	require.NoError(t, err)
	require.Equal(t, 1, ended.Ended)
	require.Equal(t, "SOLD_OUT", status(t, s, ev, id))

	hb, err := s.Heartbeat(ctx, ev, vis, gen, now)
	require.NoError(t, err)
	assert.Equal(t, outbound.HeartbeatNotFound, hb.Tag, "heartbeat tidak boleh menghidupkan entry SOLD_OUT")
	assert.Equal(t, "SOLD_OUT", status(t, s, ev, id))

	// Join 4b (join.lua): the generation record is gone (serverGen 0) while vlast still names the ended entry. A SOLD_OUT
	// entry must not be handed back as EXISTING; the fresh join is created with the same token.
	require.NoError(t, s.c.Do(ctx, s.c.B().Del().Key(vtokKey(s, ev, vis)).Build()).Error())
	joined, err := s.Join(ctx, outbound.JoinParams{
		EventID: ev, EntryID: "entry-fresh", NewVisitorID: vis, TokenVisitorID: vis, TokenGen: gen,
		AccountID: acct, NowMs: now, OneActivePerVisitor: true,
	})
	require.NoError(t, err)
	assert.Equal(t, outbound.JoinCreated, joined.Outcome)
	assert.Equal(t, "entry-fresh", joined.EntryID, "join dengan token yang sama membuat entry baru")
	assert.Equal(t, "WAITING", hget(t, s, s.kEntry(ev, "entry-fresh"), "status"))

	_, err = s.Claim(ctx, ev, id, vis, now)
	assert.ErrorIs(t, err, entity.ErrSoldOut)

	lr, err := s.Leave(ctx, ev, id, vis, now)
	require.NoError(t, err)
	assert.Equal(t, "IGNORED", lr.Tag)
	assert.Equal(t, "SOLD_OUT", lr.PrevStatus)
	assert.Equal(t, "SOLD_OUT", status(t, s, ev, id), "leave tidak boleh menimpa SOLD_OUT dengan LEFT")
}
