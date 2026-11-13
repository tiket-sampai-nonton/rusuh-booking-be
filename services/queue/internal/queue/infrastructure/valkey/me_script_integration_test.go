//go:build integration

package valkey

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/valueobject"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// multiCallMe is the former multi-call path of `me`: CheckTokenGen, FindByVisitor (then FindLastByVisitor) and
// LivePosition, each its own round-trip. me.lua must return the same outcome in one EVAL.
func multiCallMe(t *testing.T, s *QueueStore, ev, vis string, gen int64) outbound.MyEntryResult {
	t.Helper()
	ctx := context.Background()
	tg, err := s.CheckTokenGen(ctx, ev, vis, gen)
	require.NoError(t, err)
	switch tg {
	case outbound.TokenGenAbsent:
		return outbound.MyEntryResult{Tag: outbound.MyEntryAbsent}
	case outbound.TokenGenInvalid:
		return outbound.MyEntryResult{Tag: outbound.MyEntryInvalid}
	}
	ent, ok, err := s.FindByVisitor(ctx, ev, vis)
	require.NoError(t, err)
	if !ok {
		ent, ok, err = s.FindLastByVisitor(ctx, ev, vis)
		require.NoError(t, err)
		if !ok {
			return outbound.MyEntryResult{Tag: outbound.MyEntryNotFound}
		}
	}
	res := outbound.MyEntryResult{Tag: outbound.MyEntryOK, Entry: ent}
	if ent.Status == valueobject.StatusWaiting || ent.Status == valueobject.StatusStale {
		res.Position, err = s.LivePosition(ctx, ev, ent.Seq.Int64())
		require.NoError(t, err)
	}
	return res
}

// C8.9 (PF-P2-3): me.lua answers exactly what the multi-call path answers for WAITING, ADMITTED, ENDED, absent and
// invalid-generation visitors, in one script.
func TestMyEntryScript_MatchesMultiCallResult(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	ev := evID(t)
	now := time.Now().UnixMilli()

	waiting := vjoin(t, s, ev, outbound.JoinParams{EntryID: "e-wait", NewVisitorID: "vis-wait", NowMs: now})
	ahead := vjoin(t, s, ev, outbound.JoinParams{EntryID: "e-ahead", NewVisitorID: "vis-ahead", NowMs: now})
	admitted := vjoin(t, s, ev, outbound.JoinParams{EntryID: "e-adm", NewVisitorID: "vis-adm", NowMs: now})
	promoteOne(t, s, ev, "e-adm", "alloc-adm", now+60_000, now, 1)
	ended := vjoin(t, s, ev, outbound.JoinParams{EntryID: "e-end", NewVisitorID: "vis-end", NowMs: now})
	_, err := s.Leave(ctx, ev, "e-end", ended.VisitorID, now)
	require.NoError(t, err)

	tests := []struct {
		name string
		vis  string
		gen  int64
		tag  outbound.MyEntryTag
	}{
		{"waiting", waiting.VisitorID, waiting.Gen, outbound.MyEntryOK},
		{"waiting behind another entry", ahead.VisitorID, ahead.Gen, outbound.MyEntryOK},
		{"admitted", admitted.VisitorID, admitted.Gen, outbound.MyEntryOK},
		{"ended (LEFT) is returned from vlast", ended.VisitorID, ended.Gen, outbound.MyEntryOK},
		{"never joined -> absent", "vis-never", 1, outbound.MyEntryAbsent},
		{"generation 0 -> invalid", waiting.VisitorID, 0, outbound.MyEntryInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want := multiCallMe(t, s, ev, tt.vis, tt.gen)
			got, err := s.MyEntry(ctx, ev, tt.vis, tt.gen)
			require.NoError(t, err)
			assert.Equal(t, tt.tag, got.Tag)
			assert.Equal(t, want.Tag, got.Tag)
			assert.Equal(t, want.Entry, got.Entry)
			assert.Equal(t, want.Position, got.Position)
		})
	}
	t.Run("waiting entry reports its live position", func(t *testing.T) {
		got, err := s.MyEntry(ctx, ev, ahead.VisitorID, ahead.Gen)
		require.NoError(t, err)
		assert.EqualValues(t, 2, got.Position, "e-wait is live and ahead of e-ahead: position 1 + 1")
	})
}

// storedGen reads the generation stored under a vtok key.
func storedGen(t *testing.T, s *QueueStore, key string) string {
	t.Helper()
	v, err := s.c.Do(context.Background(), s.c.B().Get().Key(key).Build()).ToString()
	require.NoError(t, err)
	return v
}

// C8.9 (D-C6.1): a generation higher than the stored one is valid, and me.lua ratchets the stored generation up in the same
// EVAL. The token's TTL is kept (KEEPTTL): only the time that passed is lost.
func TestMyEntryScript_HigherGenerationRatchetsKeepingTTL(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	ev := evID(t)
	j := vjoin(t, s, ev, outbound.JoinParams{EntryID: "e-ratchet", NewVisitorID: "vis-ratchet", NowMs: time.Now().UnixMilli()})
	require.EqualValues(t, 1, j.Gen)
	key := vtokKey(s, ev, j.VisitorID)
	before := pttl(t, s, key)
	require.Greater(t, before, int64(0))

	got, err := s.MyEntry(ctx, ev, j.VisitorID, j.Gen+1)
	require.NoError(t, err)
	assert.Equal(t, outbound.MyEntryOK, got.Tag)
	assert.Equal(t, "2", storedGen(t, s, key), "the higher generation is stored")
	after := pttl(t, s, key)
	assert.Greater(t, after, int64(0), "the token keeps a TTL")
	assert.InDelta(t, before, after, 5_000, "KEEPTTL: the TTL is not reset")
}

// C8.9 (D-C6.1): a generation lower than the stored one is INVALID (401), and it changes nothing.
func TestMyEntryScript_LowerGenerationIsInvalid(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	ev := evID(t)
	j := vjoin(t, s, ev, outbound.JoinParams{EntryID: "e-lower", NewVisitorID: "vis-lower", NowMs: time.Now().UnixMilli()})
	key := vtokKey(s, ev, j.VisitorID)

	ratchet, err := s.MyEntry(ctx, ev, j.VisitorID, 2)
	require.NoError(t, err)
	require.Equal(t, outbound.MyEntryOK, ratchet.Tag)

	got, err := s.MyEntry(ctx, ev, j.VisitorID, 1)
	require.NoError(t, err)
	assert.Equal(t, outbound.MyEntryInvalid, got.Tag)
	assert.Equal(t, "2", storedGen(t, s, key), "a lower generation does not lower the stored one")
}

// C8.9: a visitor who never joined is ABSENT (404) and me.lua creates no vtok key for it.
func TestMyEntryScript_AbsentVisitorCreatesNoKey(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	ev := evID(t)
	const vis = "vis-never-joined"
	key := vtokKey(s, ev, vis)

	got, err := s.MyEntry(ctx, ev, vis, 3)
	require.NoError(t, err)
	assert.Equal(t, outbound.MyEntryAbsent, got.Tag)
	n, err := s.c.Do(ctx, s.c.B().Exists().Key(key).Build()).AsInt64()
	require.NoError(t, err)
	assert.EqualValues(t, 0, n, "no vtok key is created for an absent visitor")
}
