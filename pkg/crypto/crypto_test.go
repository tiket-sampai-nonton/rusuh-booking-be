package crypto

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const hexKey = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"

func TestParseKey(t *testing.T) {
	k, err := ParseKey(hexKey)
	require.NoError(t, err)
	assert.Len(t, k, 32)
	_, err = ParseKey("zz")
	assert.Error(t, err)
	_, err = ParseKey("00ff")
	assert.ErrorContains(t, err, "32 byte")
}

func TestHasher(t *testing.T) {
	k, _ := ParseKey(hexKey)
	h, err := NewHasher(k)
	require.NoError(t, err)
	assert.Equal(t, h.Hash("a@b.id"), h.Hash("a@b.id"))
	assert.NotEqual(t, h.Hash("a@b.id"), h.Hash("c@b.id"))
	assert.Len(t, h.Hash("x"), 32)
	_, err = NewHasher([]byte("short"))
	assert.Error(t, err)
}

func TestSealer_RoundTripAndTamper(t *testing.T) {
	k, _ := ParseKey(hexKey)
	s, err := NewSealer(k)
	require.NoError(t, err)

	ct, err := s.Seal([]byte("3174010101900001"), []byte("accounts.nik"))
	require.NoError(t, err)
	ct2, _ := s.Seal([]byte("3174010101900001"), []byte("accounts.nik"))
	assert.NotEqual(t, ct, ct2, "nonce acak: ciphertext berbeda setiap kali")

	pt, err := s.Open(ct, []byte("accounts.nik"))
	require.NoError(t, err)
	assert.Equal(t, "3174010101900001", string(pt))

	_, err = s.Open(ct, []byte("accounts.email"))
	assert.ErrorIs(t, err, ErrDecrypt)
	ct[len(ct)-1] ^= 0xff
	_, err = s.Open(ct, []byte("accounts.nik"))
	assert.ErrorIs(t, err, ErrDecrypt)
	_, err = s.Open([]byte{1, 2}, nil)
	assert.ErrorIs(t, err, ErrDecrypt)
	_, err = NewSealer([]byte("short"))
	assert.Error(t, err)
}

func TestMasking(t *testing.T) {
	assert.Equal(t, "ar***@gmail.com", MaskEmail("arnu@gmail.com"))
	assert.Equal(t, "a***@x.id", MaskEmail("ab@x.id"))
	assert.Equal(t, "***", MaskEmail("nope"))
	assert.Equal(t, "3174********0001", MaskNIK("3174010101900001"))
	assert.Equal(t, strings.Repeat("*", 16), MaskNIK("123"))
}
