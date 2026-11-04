//go:build integration

package valkey

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// C6: a WAITING entry whose email cannot be opened is counted; the counter is guarded by status and never
// creates a hash, so an old-layout entry (no unreadableN field) keeps working.
func TestNoteUnreadable_CountsOnlyWaitingEntries(t *testing.T) {
	s := mustStore(t)
	ctx := context.Background()
	ev := evID(t)
	now := time.Now().UnixMilli()
	id, _, _ := joinOne(t, s, ev, 1, now)
	assert.Empty(t, hget(t, s, s.kEntry(ev, id), "unreadableN"), "entry lama tidak punya field ini")

	for want := 1; want <= 3; want++ {
		n, err := s.NoteUnreadable(ctx, ev, id)
		require.NoError(t, err)
		assert.Equal(t, want, n)
	}

	for i, st := range []string{"ENDED", "ADMITTED", "LEFT"} {
		other, _, _ := joinOne(t, s, ev, 10+i, now)
		hsetRaw(t, s, s.kEntry(ev, other), "status", st)
		n, err := s.NoteUnreadable(ctx, ev, other)
		require.NoError(t, err)
		assert.Equal(t, -1, n, st)
		assert.Empty(t, hget(t, s, s.kEntry(ev, other), "unreadableN"), "field tidak boleh berubah untuk %s", st)
	}

	n, err := s.NoteUnreadable(ctx, ev, "tidak-ada")
	require.NoError(t, err)
	assert.Equal(t, -1, n)
	assert.False(t, keyExists(t, s, s.kEntry(ev, "tidak-ada")), "tidak pernah membuat hash baru")
}
