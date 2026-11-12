package security_test

import (
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/crypto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/infrastructure/security"
)

func keys() (hash, enc []byte) {
	hash, enc = make([]byte, 32), make([]byte, 32)
	for i := range hash {
		hash[i], enc[i] = byte(i+1), byte(200-i)
	}
	return
}

// The buyer hash at hold must equal the hash User Service produces (same HMAC key, same "nik:" prefix),
// so that claims.NIKHash from the access token can be compared against buyer_nik_hash in DB.
func TestBuyerPII_HashEqualsKeyedHMAC(t *testing.T) {
	h, e := keys()
	p, err := security.NewPII(h, e)
	require.NoError(t, err)
	ref, err := crypto.NewHasher(h)
	require.NoError(t, err)
	assert.Equal(t, ref.Hash("nik:3174010101010009"), p.HashNIK("3174010101010009"))
}

func TestBuyerPII_SealBuyerEmailOpensWithNotificationAAD(t *testing.T) {
	h, e := keys()
	p, err := security.NewPII(h, e)
	require.NoError(t, err)
	sealed, err := p.SealBuyerEmail("buyer@example.com")
	require.NoError(t, err)
	assert.NotContains(t, sealed, "buyer@example.com")

	raw, err := base64.StdEncoding.DecodeString(sealed)
	require.NoError(t, err)
	s, err := crypto.NewSealer(e)
	require.NoError(t, err)
	out, err := s.Open(raw, []byte(crypto.AADBuyerEmail))
	require.NoError(t, err)
	assert.Equal(t, "buyer@example.com", string(out))
	_, err = s.Open(raw, []byte("some.other.aad"))
	assert.Error(t, err, "bound to the buyer-email context")
}

func TestBuyerPII_FingerprintKeyedAndStable(t *testing.T) {
	h, e := keys()
	p, _ := security.NewPII(h, e)
	a := p.Fingerprint([]byte("payload"))
	assert.Equal(t, a, p.Fingerprint([]byte("payload")))
	assert.NotEqual(t, a, p.Fingerprint([]byte("payload2")))
	assert.NotContains(t, string(a), "payload")
}
