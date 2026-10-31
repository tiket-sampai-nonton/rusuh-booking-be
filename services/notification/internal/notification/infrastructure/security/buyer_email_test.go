package security_test

import (
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/crypto"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/infrastructure/security"
)

// What Queue seals (AES-GCM, AAD crypto.AADBuyerEmail, base64) is what Notification opens, and nothing else opens.
func TestUnsealBuyerEmail_OpensWhatQueueSealed(t *testing.T) {
	enc := make([]byte, 32)
	hash := make([]byte, 32)
	hash[0] = 1
	pii, err := security.NewPII(enc, hash)
	require.NoError(t, err)

	key, err := crypto.NewSealer(enc)
	require.NoError(t, err)
	sealed, err := key.Seal([]byte("pembeli@contoh.id"), []byte(crypto.AADBuyerEmail))
	require.NoError(t, err)
	b64 := base64.StdEncoding.EncodeToString(sealed)

	got, err := pii.UnsealBuyerEmail(b64)
	require.NoError(t, err)
	assert.Equal(t, "pembeli@contoh.id", got)

	wrongAAD, _ := key.Seal([]byte("pembeli@contoh.id"), []byte("users.accounts.email"))
	_, err = pii.UnsealBuyerEmail(base64.StdEncoding.EncodeToString(wrongAAD))
	assert.Error(t, err, "ciphertext untuk konteks lain tidak boleh terbuka")
	_, err = pii.UnsealBuyerEmail("%%%bukan-base64")
	assert.Error(t, err)
	assert.NotContains(t, b64, "pembeli")
}
