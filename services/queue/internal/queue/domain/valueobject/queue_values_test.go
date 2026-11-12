package valueobject_test

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/valueobject"
)

func TestStatus_CanTransitionTo(t *testing.T) {
	tests := []struct {
		name string
		from valueobject.Status
		to   valueobject.Status
		want bool
	}{
		{"waiting->admitted", valueobject.StatusWaiting, valueobject.StatusAdmitted, true},
		{"waiting->left", valueobject.StatusWaiting, valueobject.StatusLeft, true},
		{"waiting->soldout", valueobject.StatusWaiting, valueobject.StatusSoldOut, true},
		{"waiting->claimed illegal", valueobject.StatusWaiting, valueobject.StatusClaimed, false},
		{"admitted->claimed", valueobject.StatusAdmitted, valueobject.StatusClaimed, true},
		{"admitted->missed", valueobject.StatusAdmitted, valueobject.StatusMissed, true},
		{"admitted->left", valueobject.StatusAdmitted, valueobject.StatusLeft, true},
		{"claimed->waiting illegal", valueobject.StatusClaimed, valueobject.StatusWaiting, false},
		{"claimed->completed", valueobject.StatusClaimed, valueobject.StatusCompleted, true},
		{"claimed->ended", valueobject.StatusClaimed, valueobject.StatusEnded, true},
		{"terminal missed->any", valueobject.StatusMissed, valueobject.StatusWaiting, false},
		{"completed->ended illegal", valueobject.StatusCompleted, valueobject.StatusEnded, false},
		{"same status not a transition", valueobject.StatusWaiting, valueobject.StatusWaiting, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.from.CanTransitionTo(tt.to))
		})
	}
}

func TestStatus_IsTerminal(t *testing.T) {
	terminal := []valueobject.Status{
		valueobject.StatusMissed, valueobject.StatusLeft, valueobject.StatusSoldOut,
		valueobject.StatusClosed, valueobject.StatusCompleted, valueobject.StatusEnded,
	}
	for _, s := range terminal {
		assert.True(t, s.IsTerminal(), string(s))
	}
	nonTerminal := []valueobject.Status{valueobject.StatusWaiting, valueobject.StatusAdmitted, valueobject.StatusClaimed}
	for _, s := range nonTerminal {
		assert.False(t, s.IsTerminal(), string(s))
	}
}

func TestNewSeq(t *testing.T) {
	s, err := valueobject.NewSeq(5)
	require.NoError(t, err)
	assert.Equal(t, int64(5), s.Int64())

	_, err = valueobject.NewSeq(0)
	require.ErrorIs(t, err, valueobject.ErrInvalidSeq)
	_, err = valueobject.NewSeq(-1)
	require.ErrorIs(t, err, valueobject.ErrInvalidSeq)
}

func TestNewUUID_IsV7(t *testing.T) {
	v7 := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	seen := map[string]bool{}
	before := time.Now().UnixMilli()
	for i := 0; i < 1000; i++ {
		id := valueobject.NewUUID()
		assert.Regexp(t, v7, id)
		seen[id] = true
	}
	assert.Len(t, seen, 1000, "unik")
	ms, err := strconv.ParseInt(strings.ReplaceAll(valueobject.NewUUID()[:13], "-", ""), 16, 64)
	require.NoError(t, err)
	assert.InDelta(t, before, ms, 2000, "48 bit pertama adalah waktu Unix dalam milidetik")
}

func TestNewIDs(t *testing.T) {
	id := valueobject.NewUUID()
	v, err := valueobject.NewVisitorID(id)
	require.NoError(t, err)
	assert.Equal(t, id, v.String())

	e, err := valueobject.NewEntryID(id)
	require.NoError(t, err)
	assert.Equal(t, id, e.String())

	_, err = valueobject.NewVisitorID("not-a-uuid")
	require.ErrorIs(t, err, valueobject.ErrInvalidID)
	_, err = valueobject.NewEntryID("")
	require.ErrorIs(t, err, valueobject.ErrInvalidID)
}
