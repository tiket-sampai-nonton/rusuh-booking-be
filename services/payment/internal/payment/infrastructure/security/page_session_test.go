package security_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/token"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/infrastructure/security"
)

var t0 = time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC)

func newSession(t *testing.T) (*security.PageSession, ed25519.PrivateKey) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	s, err := security.NewPageSession(priv, "payment-1", "rbt-payment")
	require.NoError(t, err)
	return s, priv
}

func TestPageSession_SignThenVerifyBindsVisitorAndBooking(t *testing.T) {
	s, _ := newSession(t)
	tok, exp, err := s.Sign("visitor-1", "booking-1", t0)
	require.NoError(t, err)
	assert.Equal(t, t0.Add(security.PageSessionTTL), exp)

	visitor, booking, err := s.Verify(tok, t0.Add(time.Minute))
	require.NoError(t, err)
	assert.Equal(t, "visitor-1", visitor)
	assert.Equal(t, "booking-1", booking)
}

func TestPageSession_RejectsExpiredForeignAndWrongAudienceTokens(t *testing.T) {
	s, priv := newSession(t)
	tok, _, _ := s.Sign("visitor-1", "booking-1", t0)

	_, _, err := s.Verify(tok, t0.Add(security.PageSessionTTL+time.Minute))
	assert.ErrorIs(t, err, token.ErrExpired)

	other, _ := newSession(t)
	_, _, err = other.Verify(tok, t0)
	assert.Error(t, err, "token dari kunci lain")

	// Service token Payment yang ditandatangani kunci yang sama TIDAK boleh diterima sebagai cookie (audience berbeda).
	signer, err := token.NewSigner(priv, "payment-1")
	require.NoError(t, err)
	svc, err := signer.Sign(token.Claims{Issuer: "rbt-payment", Subject: "rbt-payment", Audience: "rbt-booking", Scope: "booking:booking-1", IssuedAt: t0.Unix(), ExpiresAt: t0.Add(time.Minute).Unix()})
	require.NoError(t, err)
	_, _, err = s.Verify(svc, t0)
	assert.ErrorIs(t, err, token.ErrClaims)

	// Token tanpa scope booking.
	noScope, err := signer.Sign(token.Claims{Issuer: "rbt-payment", Subject: "v", Audience: security.PageAudience, IssuedAt: t0.Unix(), ExpiresAt: t0.Add(time.Minute).Unix()})
	require.NoError(t, err)
	_, _, err = s.Verify(noScope, t0)
	assert.ErrorIs(t, err, token.ErrClaims)

	_, _, err = s.Verify("sampah", t0)
	assert.Error(t, err)
}

func TestNewPageSession_RejectsBadKey(t *testing.T) {
	_, err := security.NewPageSession(ed25519.PrivateKey("pendek"), "k", "iss")
	assert.Error(t, err)
}
