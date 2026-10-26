package mapper_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/mapper"
	vo "github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/valueobject"
)

func TestAttemptToDTO_OnlyContractFields(t *testing.T) {
	exp := time.Date(2026, 10, 26, 3, 0, 0, 0, time.UTC)
	a := entity.Attempt{
		ID: "secret-internal-id", BookingID: "b", AttemptNo: 2, Method: vo.MethodVABCA, ProviderOrderID: "RB-1-2", ProviderTxID: "tx",
		Status: vo.AttemptPending, AmountMinor: 1500000, ExpiresAt: exp, BookingVersion: 3,
		Instruction: &entity.Instruction{Type: "VA", Bank: "bca", VANumber: "812785002530231"},
	}
	raw, err := json.Marshal(mapper.AttemptToDTO(a))
	require.NoError(t, err)
	assert.JSONEq(t, `{"attemptNo":2,"method":"VA_BCA","status":"PENDING","bookingVersion":3,"expiresAt":"2026-10-26T03:00:00Z",
		"instruction":{"type":"VA","bank":"bca","vaNumber":"812785002530231"}}`, string(raw), "tanpa id internal, order id, atau tx id provider")
}

func TestAttemptPtrToDTO_NilStaysNil(t *testing.T) {
	assert.Nil(t, mapper.AttemptPtrToDTO(nil))
	v := mapper.AttemptPtrToDTO(&entity.Attempt{AttemptNo: 1, Method: vo.MethodQRIS, Status: vo.AttemptCreated})
	require.NotNil(t, v)
	assert.Nil(t, v.ExpiresAt, "ExpiresAt nol dihilangkan")
}
