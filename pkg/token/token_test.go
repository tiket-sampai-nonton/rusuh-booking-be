package token

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func keys(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	return pub, priv
}

var now = time.Date(2026, 10, 19, 5, 0, 0, 0, time.UTC)

func claims() Claims {
	return Claims{Issuer: "rbt-user", Subject: "acc-1", Audience: "rbt", Scope: "buyer",
		IssuedAt: now.Unix(), ExpiresAt: now.Add(15 * time.Minute).Unix()}
}

func TestSignVerify_RoundTrip(t *testing.T) {
	pub, priv := keys(t)
	s, err := NewSigner(priv, "k1")
	require.NoError(t, err)
	v, err := NewVerifier(map[string]ed25519.PublicKey{"k1": pub}, "rbt-user", "rbt", 5*time.Second)
	require.NoError(t, err)

	tok, err := s.Sign(claims())
	require.NoError(t, err)
	got, err := v.Verify(tok, now.Add(time.Minute))
	require.NoError(t, err)
	assert.Equal(t, "acc-1", got.Subject)
	assert.Equal(t, "buyer", got.Scope)
}

func TestVerify_Failures(t *testing.T) {
	pub, priv := keys(t)
	otherPub, otherPriv := keys(t)
	s, _ := NewSigner(priv, "k1")
	v, _ := NewVerifier(map[string]ed25519.PublicKey{"k1": pub}, "rbt-user", "rbt", 0)
	good, _ := s.Sign(claims())

	foreign, _ := NewSigner(otherPriv, "k1")
	forged, _ := foreign.Sign(claims())
	unknownKid, _ := NewSigner(otherPriv, "k9")
	unknown, _ := unknownKid.Sign(claims())
	_ = otherPub

	wrongAud := claims()
	wrongAud.Audience = "other"
	wa, _ := s.Sign(wrongAud)
	notYet := claims()
	notYet.NotBefore = now.Add(time.Hour).Unix()
	nb, _ := s.Sign(notYet)

	parts := strings.Split(good, ".")
	tampered := parts[0] + "." + b64.EncodeToString([]byte(`{"iss":"rbt-user","sub":"admin","aud":"rbt","iat":1,"exp":9999999999}`)) + "." + parts[2]

	tests := []struct {
		name string
		tok  string
		at   time.Time
		want error
	}{
		{"expired", good, now.Add(16 * time.Minute), ErrExpired},
		{"forged signature", forged, now, ErrSignature},
		{"unknown kid", unknown, now, ErrSignature},
		{"tampered payload", tampered, now, ErrSignature},
		{"wrong audience", wa, now, ErrClaims},
		{"not before", nb, now, ErrClaims},
		{"two parts", "a.b", now, ErrMalformed},
		{"garbage header", "!!.b.c", now, ErrMalformed},
		{"alg none", b64.EncodeToString([]byte(`{"alg":"none","kid":"k1"}`)) + ".e30.", now, ErrMalformed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := v.Verify(tt.tok, tt.at)
			assert.ErrorIs(t, err, tt.want)
		})
	}
}

func TestConstructorsValidate(t *testing.T) {
	pub, priv := keys(t)
	_, err := NewSigner(priv[:10], "k1")
	assert.Error(t, err)
	_, err = NewSigner(priv, "")
	assert.Error(t, err)
	_, err = NewVerifier(nil, "i", "a", 0)
	assert.Error(t, err)
	_, err = NewVerifier(map[string]ed25519.PublicKey{"k": pub[:3]}, "i", "a", 0)
	assert.Error(t, err)
	_, err = NewVerifier(map[string]ed25519.PublicKey{"k": pub}, "", "a", 0)
	assert.Error(t, err)

	s, _ := NewSigner(priv, "k1")
	bad := claims()
	bad.ExpiresAt = bad.IssuedAt
	_, err = s.Sign(bad)
	assert.ErrorIs(t, err, ErrClaims)
}

func TestPEMParsing(t *testing.T) {
	pub, priv := keys(t)
	privDER, err := x509.MarshalPKCS8PrivateKey(priv)
	require.NoError(t, err)
	pubDER, err := x509.MarshalPKIXPublicKey(pub)
	require.NoError(t, err)

	gotPriv, err := ParsePrivateKeyPEM(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privDER}))
	require.NoError(t, err)
	assert.True(t, gotPriv.Equal(priv))
	gotPub, err := ParsePublicKeyPEM(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}))
	require.NoError(t, err)
	assert.True(t, gotPub.Equal(pub))

	_, err = ParsePrivateKeyPEM([]byte("nope"))
	assert.Error(t, err)
	_, err = ParsePublicKeyPEM([]byte("nope"))
	assert.Error(t, err)
	_, err = ParsePrivateKeyPEM(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte{1, 2}}))
	assert.Error(t, err)
	_, err = ParsePublicKeyPEM(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: []byte{1, 2}}))
	assert.Error(t, err)
}
