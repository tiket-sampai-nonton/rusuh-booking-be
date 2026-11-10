package valueobject

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewEventID(t *testing.T) {
	const ok = "11111111-1111-1111-1111-111111111111"
	id, err := NewEventID(ok)
	require.NoError(t, err)
	assert.Equal(t, ok, id.String())
	for _, bad := range []string{"", "nope", "1111111111111111111111111111111111", ok + "x", "g1111111-1111-1111-1111-111111111111"} {
		_, err := NewEventID(bad)
		assert.ErrorIs(t, err, ErrInvalidID, bad)
	}
}
