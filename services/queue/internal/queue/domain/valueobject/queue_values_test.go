package valueobject_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
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

func TestZoneFor(t *testing.T) {
	tests := []struct {
		name                  string
		seq, guaranteed, poss valueobject.Seq
		want                  valueobject.Zone
	}{
		{"within guaranteed", 100, 150, 200, valueobject.ZoneGuaranteed},
		{"at guaranteed boundary", 150, 150, 200, valueobject.ZoneGuaranteed},
		{"within possible", 175, 150, 200, valueobject.ZonePossible},
		{"at possible boundary", 200, 150, 200, valueobject.ZonePossible},
		{"beyond possible", 201, 150, 200, valueobject.ZoneNotEnough},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, valueobject.ZoneFor(tt.seq, tt.guaranteed, tt.poss))
		})
	}
}

func TestValidateNIKFormat(t *testing.T) {
	tests := []struct {
		name    string
		nik     string
		wantErr bool
	}{
		{"valid 16 digit", "3201234567890123", false},
		{"letters", "32012345678901ab", true},
		{"too short", "320123456789012", true},
		{"too long", "32012345678901234", true},
		{"empty", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := valueobject.ValidateNIKFormat(tt.nik)
			if tt.wantErr {
				require.ErrorIs(t, err, entity.ErrNikInvalid)
			} else {
				require.NoError(t, err)
			}
		})
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

func TestBuyerNikHash_Hex(t *testing.T) {
	h := valueobject.BuyerNikHash([]byte{0xde, 0xad, 0xbe, 0xef})
	assert.Equal(t, "deadbeef", h.Hex())
}
