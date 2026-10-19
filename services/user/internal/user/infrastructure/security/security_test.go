package security

import (
	"crypto/ed25519"
	"crypto/rand"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/token"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/port/inbound"
)

func key(b byte) []byte {
	k := make([]byte, 32)
	for i := range k {
		k[i] = b + byte(i)
	}
	return k
}

func TestPII(t *testing.T) {
	p, err := NewPII(key(1), key(50), key(100))
	require.NoError(t, err)

	assert.Equal(t, p.HashEmail("a@b.id"), p.HashEmail("a@b.id"))
	assert.NotEqual(t, p.HashEmail("3174010101900001"), p.HashNIK("3174010101900001"), "domain separation email vs nik")
	assert.NotEqual(t, p.HashOTP(p.HashEmail("a@b.id"), "123456"), p.HashOTP(p.HashEmail("c@b.id"), "123456"))
	assert.Len(t, p.HashRefreshToken("x"), 32)

	enc, err := p.SealEmail("a@b.id")
	require.NoError(t, err)
	assert.NotContains(t, string(enc), "a@b.id")
	nikEnc, err := p.SealNIK("3174010101900001")
	require.NoError(t, err)
	assert.NotEmpty(t, nikEnc)

	_, err = NewPII(key(1)[:5], key(2), key(3))
	assert.Error(t, err)
	_, err = NewPII(key(1), key(2)[:5], key(3))
	assert.Error(t, err)
	_, err = NewPII(key(1), key(2), key(3)[:5])
	assert.Error(t, err)
}

func TestSecrets(t *testing.T) {
	re := regexp.MustCompile(`^\d{6}$`)
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		c, err := Secrets{}.OTPCode()
		require.NoError(t, err)
		assert.Regexp(t, re, c)
		seen[c] = true
	}
	assert.Greater(t, len(seen), 40)
	r1, _ := Secrets{}.RefreshToken()
	r2, _ := Secrets{}.RefreshToken()
	assert.Len(t, r1, 43)
	assert.NotEqual(t, r1, r2)
}

func TestTokens_IssueAndVerify(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	signer, err := token.NewSigner(priv, "user-1")
	require.NoError(t, err)
	verifier, err := token.NewVerifier(map[string]ed25519.PublicKey{"user-1": pub}, "rbt-user", "rbt", 0)
	require.NoError(t, err)

	now := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	clock := now
	tk := NewTokens(signer, verifier, "rbt-user", "rbt", 15*time.Minute, func() time.Time { return clock })

	tok, exp, err := tk.IssueAccess("acc-1", now)
	require.NoError(t, err)
	assert.Equal(t, now.Add(15*time.Minute), exp)

	id, err := tk.VerifyAccess(tok)
	require.NoError(t, err)
	assert.Equal(t, "acc-1", id)

	clock = now.Add(16 * time.Minute)
	_, err = tk.VerifyAccess(tok)
	assert.ErrorIs(t, err, inbound.ErrUnauthorized)
	_, err = tk.VerifyAccess("garbage")
	assert.ErrorIs(t, err, inbound.ErrUnauthorized)
}
