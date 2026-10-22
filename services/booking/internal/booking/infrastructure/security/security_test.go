package security_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/infrastructure/security"
)

func TestNewPII_InvalidHashKey(t *testing.T) {
	_, err := security.NewPII(nil, make([]byte, 32))
	assert.Error(t, err)
}

func TestNewPII_InvalidEncKey(t *testing.T) {
	_, err := security.NewPII(make([]byte, 32), nil)
	assert.Error(t, err)
}

func TestPII_HashAndSeal(t *testing.T) {
	pii, err := security.NewPII(make([]byte, 32), make([]byte, 32))
	require.NoError(t, err)

	hash := pii.HashNIK("3174010101900001")
	assert.NotEmpty(t, hash)

	sealed, err := pii.SealNIK("3174010101900001")
	require.NoError(t, err)
	assert.NotEmpty(t, sealed)

	// Same input → same hash
	hash2 := pii.HashNIK("3174010101900001")
	assert.Equal(t, hash, hash2)
}

func TestAdmissionVerifier_NilVerifier(t *testing.T) {
	v := security.NewAdmissionVerifier(nil, time.Now)
	_, err := v.Verify("any.token.here")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "belum dikonfigurasi")
}

func TestAdmissionVerifier_InvalidToken(t *testing.T) {
	v := security.NewAdmissionVerifier(nil, time.Now)
	_, err := v.Verify("garbage")
	assert.Error(t, err)
}

func TestBearerVerifier_NilVerifier(t *testing.T) {
	v := security.NewBearerVerifier(nil, time.Now)
	_, err := v.VerifyAccess("any.token.here")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "belum dikonfigurasi")
}

func TestMultiBearerVerifier_BothNil(t *testing.T) {
	v := security.NewMultiBearerVerifier(nil, nil, time.Now)
	_, err := v.VerifyAccess("any.token.here")
	// Both nil → both return error → final error returned
	assert.Error(t, err)
}
