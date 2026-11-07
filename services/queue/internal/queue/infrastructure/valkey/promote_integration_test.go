//go:build integration

package valkey

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

type snapshot struct {
	status         map[string]string
	inWaiting      map[string]bool
	inActive       map[string]bool
	activeCard     int64
	allocationByID map[string]string
}

func snap(t *testing.T, s *QueueStore, ev string, ids ...string) snapshot {
	t.Helper()
	sn := snapshot{status: map[string]string{}, inWaiting: map[string]bool{}, inActive: map[string]bool{}, allocationByID: map[string]string{}}
	for _, id := range ids {
		sn.status[id] = hget(t, s, s.kEntry(ev, id), "status")
		sn.allocationByID[id] = hget(t, s, s.kEntry(ev, id), "allocationId")
		_, sn.inWaiting[id] = zscore(t, s, s.kWaiting(ev), id)
		_, sn.inActive[id] = zscore(t, s, s.kActive(ev), id)
	}
	sn.activeCard = zcard(t, s, s.kActive(ev))
	return sn
}

func cand(id string, deadline int64) outbound.PromoteCandidate {
	return outbound.PromoteCandidate{EntryID: id, AllocationID: "alloc-" + id, ClaimDeadlineMs: deadline}
}

// TC-QUE-19c-LUA (REQ-QSH-09.3): the fence. A wrong token and an expired lease both give LEASE_LOST with no mutation.
func TestPromote_FenceRejectsWrongAndExpiredToken(t *testing.T) {
	s := mustStore(t)
	ctx := context.Background()
	ev := evID(t)
	now := time.Now().UnixMilli()
	a, _, _ := joinOne(t, s, ev, 1, now)
	before := snap(t, s, ev, a)

	tok, ok, err := s.AcquirePromoterLease(ctx, ev, 5*time.Second)
	require.NoError(t, err)
	require.True(t, ok)
	defer func() { _ = s.ReleasePromoterLease(ctx, ev, tok) }()

	res, err := s.Promote(ctx, ev, []outbound.PromoteCandidate{cand(a, now+60_000)}, 5, now, outbound.PromoteOpts{LeaseToken: "token-orang-lain"})
	require.NoError(t, err)
	assert.True(t, res.LeaseLost)
	assert.Empty(t, res.Promoted)
	assert.False(t, res.Full)
	assert.Equal(t, before, snap(t, s, ev, a), "LEASE_LOST tidak boleh mengubah apa pun")

	// expired TTL: the holder's own token stops working once the key lapses
	ev2 := evID(t) + "-ttl"
	b, _, _ := joinOne(t, s, ev2, 2, now)
	beforeB := snap(t, s, ev2, b)
	tok2, ok, err := s.AcquirePromoterLease(ctx, ev2, 40*time.Millisecond)
	require.NoError(t, err)
	require.True(t, ok)
	time.Sleep(80 * time.Millisecond)
	res, err = s.Promote(ctx, ev2, []outbound.PromoteCandidate{cand(b, now+60_000)}, 5, now, outbound.PromoteOpts{LeaseToken: tok2})
	require.NoError(t, err)
	assert.True(t, res.LeaseLost, "token yang TTL-nya lewat harus ditolak")
	assert.Equal(t, beforeB, snap(t, s, ev2, b))
}

func TestPromote_EmptyTokenIsAnErrorBeforeAnyValkeyCall(t *testing.T) {
	s := &QueueStore{} // nil client/keyer: any Valkey call would panic
	_, err := s.Promote(context.Background(), "ev", []outbound.PromoteCandidate{cand("x", 1)}, 1, 1, outbound.PromoteOpts{})
	require.Error(t, err)
}

// REQ-QSH-03.3/.4: requireHead refuses to promote a non-head candidate and reports NOT_HEAD without mutating.
func TestPromote_RequireHeadRefusesANonHeadCandidate(t *testing.T) {
	s := mustStore(t)
	ctx := context.Background()
	ev := evID(t)
	now := time.Now().UnixMilli()
	first, _, _ := joinOne(t, s, ev, 1, now)
	second, _, _ := joinOne(t, s, ev, 2, now)
	before := snap(t, s, ev, first, second)

	tok, _, err := s.AcquirePromoterLease(ctx, ev, 5*time.Second)
	require.NoError(t, err)
	defer func() { _ = s.ReleasePromoterLease(ctx, ev, tok) }()

	res, err := s.Promote(ctx, ev, []outbound.PromoteCandidate{cand(second, now+60_000)}, 5, now, outbound.PromoteOpts{RequireHead: true, LeaseToken: tok})
	require.NoError(t, err)
	assert.Empty(t, res.Promoted)
	require.Len(t, res.Skipped, 1)
	assert.Equal(t, outbound.PromoteSkip{EntryID: second, Status: "NOT_HEAD"}, res.Skipped[0])
	assert.Equal(t, before, snap(t, s, ev, first, second), "NOT_HEAD tidak boleh mengubah apa pun")

	res, err = s.Promote(ctx, ev, []outbound.PromoteCandidate{cand(first, now+60_000)}, 5, now, outbound.PromoteOpts{RequireHead: true, LeaseToken: tok})
	require.NoError(t, err)
	assert.Equal(t, []string{first}, res.Promoted, "head boleh dipromosikan")
}

// TC-QUE-20 (INV-1/INV-2 ordering): two joiners race for the last slot; the lower seq wins, the other sees NOT_HEAD.
func TestPromote_LowerSeqWinsTheLastSlot(t *testing.T) {
	s := mustStore(t)
	ctx := context.Background()
	ev := evID(t)
	now := time.Now().UnixMilli()
	low, _, _ := joinOne(t, s, ev, 1, now)
	high, _, _ := joinOne(t, s, ev, 2, now)
	tok, _, err := s.AcquirePromoterLease(ctx, ev, 5*time.Second)
	require.NoError(t, err)
	defer func() { _ = s.ReleasePromoterLease(ctx, ev, tok) }()

	resHigh, err := s.Promote(ctx, ev, []outbound.PromoteCandidate{cand(high, now+60_000)}, 1, now, outbound.PromoteOpts{RequireHead: true, LeaseToken: tok})
	require.NoError(t, err)
	assert.Empty(t, resHigh.Promoted, "seq lebih tinggi tidak boleh melompat")
	resLow, err := s.Promote(ctx, ev, []outbound.PromoteCandidate{cand(low, now+60_000)}, 1, now, outbound.PromoteOpts{RequireHead: true, LeaseToken: tok})
	require.NoError(t, err)
	assert.Equal(t, []string{low}, resLow.Promoted)
	assert.Equal(t, int64(1), zcard(t, s, s.kActive(ev)))
}

func TestPromote_FullReportsFullWithoutMutation(t *testing.T) {
	s := mustStore(t)
	ctx := context.Background()
	ev := evID(t)
	now := time.Now().UnixMilli()
	joinAdmit(t, s, ev, 1, now+60_000, now)
	w, _, _ := joinOne(t, s, ev, 2, now)
	tok, _, err := s.AcquirePromoterLease(ctx, ev, 5*time.Second)
	require.NoError(t, err)
	defer func() { _ = s.ReleasePromoterLease(ctx, ev, tok) }()
	res, err := s.Promote(ctx, ev, []outbound.PromoteCandidate{cand(w, now+60_000)}, 1, now, outbound.PromoteOpts{LeaseToken: tok})
	require.NoError(t, err)
	assert.True(t, res.Full)
	assert.Equal(t, "WAITING", hget(t, s, s.kEntry(ev, w), "status"))
}

// design §5.4: every candidate the script looked at but did not promote is reported with its status, so Go can
// release the Booking allocation only for provably terminal entries. Unreached candidates are not reported.
func TestPromote_SkipReportCoversTerminalAndAdmittedStatuses(t *testing.T) {
	s := mustStore(t)
	ctx := context.Background()
	ev := evID(t)
	now := time.Now().UnixMilli()
	tok, _, err := s.AcquirePromoterLease(ctx, ev, 5*time.Second)
	require.NoError(t, err)
	defer func() { _ = s.ReleasePromoterLease(ctx, ev, tok) }()

	ids := map[string]string{}
	for i, st := range []string{"LEFT", "ENDED", "COMPLETED", "MISSED"} {
		id, _, _ := joinOne(t, s, ev, 10+i, now)
		hsetRaw(t, s, s.kEntry(ev, id), "status", st)
		ids[st] = id
	}
	same, _, _ := joinOne(t, s, ev, 20, now)
	hsetRaw(t, s, s.kEntry(ev, same), "status", "ADMITTED", "allocationId", "alloc-"+same)
	other, _, _ := joinOne(t, s, ev, 21, now)
	hsetRaw(t, s, s.kEntry(ev, other), "status", "ADMITTED", "allocationId", "alloc-stored")

	cands := []outbound.PromoteCandidate{}
	for _, st := range []string{"LEFT", "ENDED", "COMPLETED", "MISSED"} {
		cands = append(cands, cand(ids[st], now+60_000))
	}
	cands = append(cands, cand(same, now+60_000), cand(other, now+60_000), cand("tidak-ada", now+60_000))

	res, err := s.Promote(ctx, ev, cands, 10, now, outbound.PromoteOpts{LeaseToken: tok})
	require.NoError(t, err)
	assert.Empty(t, res.Promoted)
	got := map[string]outbound.PromoteSkip{}
	for _, sk := range res.Skipped {
		got[sk.EntryID] = sk
	}
	for _, st := range []string{"LEFT", "ENDED", "COMPLETED", "MISSED"} {
		assert.Equal(t, st, got[ids[st]].Status, st)
	}
	assert.Equal(t, "ADMITTED_SAME", got[same].Status)
	assert.Equal(t, "ADMITTED_OTHER", got[other].Status)
	assert.Equal(t, "alloc-stored", got[other].AllocationID)
	assert.Equal(t, "NO_ENTRY", got["tidak-ada"].Status)
}

func TestPromote_UnreachedCandidatesAreNotReported(t *testing.T) {
	s := mustStore(t)
	ctx := context.Background()
	ev := evID(t)
	now := time.Now().UnixMilli()
	tok, _, err := s.AcquirePromoterLease(ctx, ev, 5*time.Second)
	require.NoError(t, err)
	defer func() { _ = s.ReleasePromoterLease(ctx, ev, tok) }()
	ids := make([]string, 3)
	cands := make([]outbound.PromoteCandidate, 3)
	for i := range ids {
		ids[i], _, _ = joinOne(t, s, ev, 30+i, now)
		cands[i] = cand(ids[i], now+60_000)
	}
	res, err := s.Promote(ctx, ev, cands, 2, now, outbound.PromoteOpts{LeaseToken: tok}) // 2 slots, 3 candidates
	require.NoError(t, err)
	assert.Equal(t, ids[:2], res.Promoted)
	assert.Empty(t, res.Skipped, "kandidat yang tak terjangkau tidak dilaporkan; tetap WAITING")
	assert.Equal(t, "WAITING", hget(t, s, s.kEntry(ev, ids[2]), "status"))
}

// A reply that is not the documented shape must surface as an error, never as "nothing promoted" (Q-03).
func TestMapPromote_MalformedReplyIsAnError(t *testing.T) {
	s := mustStore(t)
	ctx := context.Background()
	for name, body := range map[string]string{
		"hanya tag OK":       "return {'OK'}",
		"promoted bukan arr": "return {'OK', 'x', {}}",
		"tag tak dikenal":    "return {'HUH'}",
		"kosong":             "return {}",
	} {
		arr, err := s.c.Do(ctx, s.c.B().Eval().Script(body).Numkeys(0).Build()).ToArray()
		require.NoError(t, err, name)
		_, err = mapPromote(arr)
		assert.Error(t, err, name)
	}
	_ = fmt.Sprint
}
