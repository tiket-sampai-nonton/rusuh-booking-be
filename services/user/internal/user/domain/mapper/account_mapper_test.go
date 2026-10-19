package mapper

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/domain/entity"
)

func TestToTokenResponse(t *testing.T) {
	a := entity.Account{ID: "acc-1", EmailMasked: "ar***@gmail.com", NIKMasked: "3174********0001", FullName: "Arif"}
	exp := time.Date(2026, 10, 19, 7, 15, 0, 0, time.FixedZone("WIB", 7*3600))
	got := ToTokenResponse(a, "acc", exp, "ref", exp.Add(time.Hour))
	assert.Equal(t, "Bearer", got.TokenType)
	assert.Equal(t, "2026-10-19T00:15:00Z", got.AccessTokenExpiresAt)
	assert.Equal(t, "2026-10-19T01:15:00Z", got.RefreshTokenExpiresAt)
	assert.True(t, got.Account.HasNIK)
	assert.Equal(t, "Arif", got.Account.FullName)
	assert.False(t, ToProfileResponse(entity.Account{ID: "x"}).HasNIK)
}
