package security

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
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

	t.Run("without NIK", func(t *testing.T) {
		tok, exp, err := tk.IssueAccess("acc-1", "sess-1", "buyer@example.com", nil, "", now)
		require.NoError(t, err)
		assert.Equal(t, now.Add(15*time.Minute), exp)

		id, err := tk.VerifyAccess(tok)
		require.NoError(t, err)
		assert.Equal(t, "acc-1", id)

		// Verify token claims directly
		claims, err := verifier.Verify(tok, now)
		require.NoError(t, err)
		assert.Equal(t, "buyer@example.com", claims.Email)
		assert.False(t, claims.HasNIK)
		assert.Empty(t, claims.NIKHash)
	})

	t.Run("with NIK", func(t *testing.T) {
		nikHash := []byte{0xab, 0xcd, 0xef}
		tok, exp, err := tk.IssueAccess("acc-2", "sess-2", "user@example.com", nikHash, "3174********0001", now)
		require.NoError(t, err)
		assert.Equal(t, now.Add(15*time.Minute), exp)

		id, err := tk.VerifyAccess(tok)
		require.NoError(t, err)
		assert.Equal(t, "acc-2", id)

		// Verify token claims directly
		claims, err := verifier.Verify(tok, now)
		require.NoError(t, err)
		assert.Equal(t, "user@example.com", claims.Email)
		assert.True(t, claims.HasNIK)
		assert.Equal(t, "abcdef", claims.NIKHash)
		assert.Equal(t, "3174********0001", claims.NIKMasked)
	})

	t.Run("expired", func(t *testing.T) {
		tok, _, _ := tk.IssueAccess("acc-1", "sess-1", "buyer@example.com", nil, "", now)
		clock = now.Add(16 * time.Minute)
		_, err = tk.VerifyAccess(tok)
		assert.ErrorIs(t, err, inbound.ErrUnauthorized)
	})

	t.Run("garbage", func(t *testing.T) {
		_, err := tk.VerifyAccess("garbage")
		assert.ErrorIs(t, err, inbound.ErrUnauthorized)
	})
}

type stubDenylist struct {
	revoked map[string]bool
	err     error
}

func (s stubDenylist) Revoke(context.Context, string, time.Duration) error { return nil }
func (s stubDenylist) IsRevoked(_ context.Context, sid string) (bool, error) {
	return s.revoked[sid], s.err
}

func TestTokens_VerifyAccessRejectsTokensOfLoggedOutSessions(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	signer, err := token.NewSigner(priv, "user-1")
	require.NoError(t, err)
	verifier, err := token.NewVerifier(map[string]ed25519.PublicKey{"user-1": pub}, "rbt-user", "rbt", 0)
	require.NoError(t, err)
	now := time.Now()
	tk := NewTokens(signer, verifier, "rbt-user", "rbt", 15*time.Minute, func() time.Time { return now })

	tok, _, err := tk.IssueAccess("acc-1", "sess-1", "buyer@example.com", nil, "", now)
	require.NoError(t, err)
	claims, err := verifier.Verify(tok, now)
	require.NoError(t, err)
	assert.Equal(t, "sess-1", claims.SessionID, "sid ikut di access token")

	acc, err := tk.VerifyAccess(tok)
	require.NoError(t, err)
	assert.Equal(t, "acc-1", acc, "tanpa denylist token berlaku seperti sebelumnya")

	tk.WithDenylist(stubDenylist{revoked: map[string]bool{"sess-1": true}})
	_, err = tk.VerifyAccess(tok)
	assert.ErrorIs(t, err, inbound.ErrUnauthorized, "sesi sudah logout: ditolak walau exp belum lewat")

	tk.WithDenylist(stubDenylist{revoked: map[string]bool{"sess-lain": true}})
	_, err = tk.VerifyAccess(tok)
	assert.NoError(t, err, "sesi lain yang logout tidak berpengaruh")

	tk.WithDenylist(stubDenylist{err: errors.New("valkey mati")})
	_, err = tk.VerifyAccess(tok)
	assert.ErrorIs(t, err, inbound.ErrUnauthorized, "fail closed bila denylist tidak bisa dibaca")
}

func TestSecrets_SessionID(t *testing.T) {
	re := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		id, err := Secrets{}.SessionID()
		require.NoError(t, err)
		assert.Regexp(t, re, id)
		seen[id] = true
	}
	assert.Len(t, seen, 100, "unik")
}
