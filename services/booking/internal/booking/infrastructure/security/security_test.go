package security_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/infrastructure/security"
)

var key32 = make([]byte, 32)

func TestPII_HashDeterministicSealRoundtripShape(t *testing.T) {
	pii, err := security.NewPII(key32, key32)
	require.NoError(t, err)

	h1 := pii.HashNIK("3174010101010001")
	h2 := pii.HashNIK("3174010101010001")
	assert.Equal(t, h1, h2, "hash is deterministic")
	assert.NotEqual(t, pii.HashNIK("3174010101010002"), h1)

	enc, err := pii.SealNIK("3174010101010001")
	require.NoError(t, err)
	assert.NotEmpty(t, enc)
	assert.NotContains(t, string(enc), "3174010101010001")
}

func TestPII_RejectsBadKey(t *testing.T) {
	_, err := security.NewPII([]byte("short"), key32)
	assert.Error(t, err)
}

func TestVerifiers_NilGuard(t *testing.T) {
	av := security.NewAdmissionVerifier(nil, time.Now)
	_, err := av.Verify("tok")
	assert.Error(t, err)

	bv := security.NewBearerVerifier(nil, time.Now)
	_, err = bv.VerifyAccess("tok")
	assert.Error(t, err)
}
