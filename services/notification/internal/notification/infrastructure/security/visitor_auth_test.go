package security_test

import (
	"crypto/ed25519"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/token"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/infrastructure/security"
)

func TestVisitorAuthenticator_Resolve(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	signer, err := token.NewSigner(priv, "k1")
	require.NoError(t, err)
	verifier, err := token.NewVerifier(map[string]ed25519.PublicKey{"k1": pub}, "iss", "aud", time.Second)
	require.NoError(t, err)
	now := time.Now()
	auth := security.NewVisitorAuthenticator(verifier, func() time.Time { return now })

	tok, err := signer.Sign(token.Claims{
		Issuer: "iss", Subject: "visitor-1", Audience: "aud",
		IssuedAt: now.Unix(), ExpiresAt: now.Add(time.Hour).Unix(),
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Visitor-Token", tok)
	id, ok := auth.Resolve(req)
	assert.True(t, ok)
	assert.Equal(t, "visitor-1", id)
}

func TestVisitorAuthenticator_NoToken(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(nil)
	verifier, _ := token.NewVerifier(map[string]ed25519.PublicKey{"k1": pub}, "iss", "aud", time.Second)
	auth := security.NewVisitorAuthenticator(verifier, time.Now)

	_, ok := auth.Resolve(httptest.NewRequest(http.MethodGet, "/", nil))
	assert.False(t, ok)
}

func TestVisitorAuthenticator_InvalidToken(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(nil)
	verifier, _ := token.NewVerifier(map[string]ed25519.PublicKey{"k1": pub}, "iss", "aud", time.Second)
	auth := security.NewVisitorAuthenticator(verifier, time.Now)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Visitor-Token", "garbage")
	_, ok := auth.Resolve(req)
	assert.False(t, ok)
}
