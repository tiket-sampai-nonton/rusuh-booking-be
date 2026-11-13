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
