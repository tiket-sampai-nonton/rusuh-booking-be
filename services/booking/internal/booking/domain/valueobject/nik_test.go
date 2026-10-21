package valueobject_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	vo "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/valueobject"
)

// fakeProtector is a deterministic NIK protector for tests (no real crypto).
type fakeProtector struct{}

func (fakeProtector) HashNIK(nik string) []byte          { return []byte("h:" + nik) }
func (fakeProtector) SealNIK(nik string) ([]byte, error) { return []byte("e:" + nik), nil }

func TestValidNIKStructure(t *testing.T) {
	cases := []struct {
		name string
		nik  string
		want bool
	}{
		{"valid male", "3174010101010001", true},
		{"valid female +40", "3174014101010001", true},
		{"too short", "317401010101000", false},
		{"non numeric", "31740101010100AB", false},
		{"province 00 invalid", "0074010101010001", false},
		{"day 00 invalid", "3174010001010001", false},
		{"day 32 invalid", "3174013201010001", false},
		{"female day 72 invalid", "3174017201010001", false},
		{"female day 71 valid (31)", "3174017101010001", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, vo.ValidNIKStructure(tc.nik))
		})
	}
}

func TestParseNIK_InvalidRejected(t *testing.T) {
	_, err := vo.ParseNIK("bad", fakeProtector{})
	assert.ErrorIs(t, err, vo.ErrInvalidNIK)
}

func TestNIK_MaskNeverLeaksPlaintext(t *testing.T) {
	raw := "3174010101019999"
	nik, err := vo.ParseNIK(raw, fakeProtector{})
	require.NoError(t, err)

	assert.NotContains(t, nik.String(), raw)
	assert.Equal(t, "3174********9999", nik.Masked())

	b, err := json.Marshal(nik)
	require.NoError(t, err)
	assert.False(t, strings.Contains(string(b), raw), "JSON must not contain plaintext NIK")
	assert.Contains(t, string(b), "3174********9999")
}

func TestNIK_HashDeterministic(t *testing.T) {
	a, err := vo.ParseNIK("3174010101010001", fakeProtector{})
	require.NoError(t, err)
	b, err := vo.ParseNIK("3174010101010001", fakeProtector{})
	require.NoError(t, err)
	assert.Equal(t, a.Hash(), b.Hash())
	assert.Equal(t, []byte("e:3174010101010001"), a.Enc())
}

func TestParseNIKHashOnly_NoEnc(t *testing.T) {
	nik, err := vo.ParseNIKHashOnly("3174010101010001", fakeProtector{})
	require.NoError(t, err)
	assert.Nil(t, nik.Enc())
	assert.NotEmpty(t, nik.Hash())
}
