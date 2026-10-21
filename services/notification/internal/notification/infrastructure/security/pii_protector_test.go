package security_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/crypto"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/infrastructure/security"
)

func key(b byte) []byte {
	k := make([]byte, 32)
	for i := range k {
		k[i] = b
	}
	return k
}

func TestPII_UnsealEmail_MatchesUserServiceAAD(t *testing.T) {
	encKey := key(0x01)
	// Simulate the User Service sealing an email with AAD "users.accounts.email".
	sealer, err := crypto.NewSealer(encKey)
	require.NoError(t, err)
	ct, err := sealer.Seal([]byte("buyer@example.id"), []byte("users.accounts.email"))
	require.NoError(t, err)

	pii, err := security.NewPII(encKey, key(0x02))
	require.NoError(t, err)
	got, err := pii.UnsealEmail(ct)
	require.NoError(t, err)
	assert.Equal(t, "buyer@example.id", got)
}

func TestPII_UnsealOTP_MatchesUserServiceAAD(t *testing.T) {
	encKey := key(0x01)
	sealer, _ := crypto.NewSealer(encKey)
	ct, err := sealer.Seal([]byte("123456"), []byte("otp.requested.code"))
	require.NoError(t, err)

	pii, _ := security.NewPII(encKey, key(0x02))
	got, err := pii.UnsealOTP(ct)
	require.NoError(t, err)
	assert.Equal(t, "123456", got)
}

func TestPII_UnsealEmail_WrongKeyFails(t *testing.T) {
	sealer, _ := crypto.NewSealer(key(0x09))
	ct, _ := sealer.Seal([]byte("x"), []byte("users.accounts.email"))
	pii, _ := security.NewPII(key(0x01), key(0x02))
	_, err := pii.UnsealEmail(ct)
	require.Error(t, err)
}

func TestPII_PushTokenRoundTrip(t *testing.T) {
	pii, _ := security.NewPII(key(0x01), key(0x02))
	enc, err := pii.SealPushToken("device-token")
	require.NoError(t, err)
	got, err := pii.UnsealPushToken(enc)
	require.NoError(t, err)
	assert.Equal(t, "device-token", got)
}

func TestPII_HMACRecipient_Deterministic(t *testing.T) {
	pii, _ := security.NewPII(key(0x01), key(0x02))
	a := pii.HMACRecipient("buyer@example.id")
	b := pii.HMACRecipient("buyer@example.id")
	assert.Equal(t, a, b)
	assert.NotEqual(t, a, pii.HMACRecipient("other@example.id"))
	assert.NotEmpty(t, a)
}
