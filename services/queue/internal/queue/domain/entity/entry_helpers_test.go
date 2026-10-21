package entity_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/valueobject"
)

func TestEntry_HexHelpers(t *testing.T) {
	e := entity.Entry{BuyerNikHash: []byte{0xde, 0xad}, EmailHash: []byte{0xbe, 0xef}}
	assert.Equal(t, "dead", e.BuyerNikHashHex())
	assert.Equal(t, "beef", e.EmailHashHex())
}

func TestEntry_IsTerminal(t *testing.T) {
	assert.False(t, entity.Entry{Status: valueobject.StatusWaiting}.IsTerminal())
	assert.True(t, entity.Entry{Status: valueobject.StatusCompleted}.IsTerminal())
	assert.True(t, entity.Entry{Status: valueobject.StatusLeft}.IsTerminal())
}
