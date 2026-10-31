package security_test

import (
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/crypto"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/infrastructure/security"
)

// Queue seals the buyer email once for Booking and Notification: ciphertext only, base64, bound to AADBuyerEmail.
func TestSealBuyerEmail_IsCiphertextBoundToTheBuyerEmailContext(t *testing.T) {
	enc, hash := make([]byte, 32), make([]byte, 32)
	hash[0] = 1
	p, err := security.NewPII(enc, hash)
	require.NoError(t, err)

	b64, err := p.SealBuyerEmail("pembeli@contoh.id")
	require.NoError(t, err)
	assert.NotContains(t, b64, "pembeli")
	raw, err := base64.StdEncoding.DecodeString(b64)
	require.NoError(t, err)

	s, err := crypto.NewSealer(enc)
	require.NoError(t, err)
	out, err := s.Open(raw, []byte(crypto.AADBuyerEmail))
	require.NoError(t, err)
	assert.Equal(t, "pembeli@contoh.id", string(out))
	_, err = s.Open(raw, []byte("queue.email"))
	assert.Error(t, err, "bukan ciphertext konteks antrean")

	again, _ := p.SealBuyerEmail("pembeli@contoh.id")
	assert.NotEqual(t, b64, again, "nonce acak: ciphertext berbeda tiap kali")
}
