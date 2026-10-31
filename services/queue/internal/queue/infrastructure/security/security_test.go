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

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/infrastructure/security"
)

var (
	encKey  = bytesSeq(0x00)
	hashKey = bytesSeq(0x10)
)

func bytesSeq(start byte) []byte {
	b := make([]byte, 32)
	for i := range b {
		b[i] = start + byte(i)
	}
	return b
}

func TestPII_HashIsDeterministic(t *testing.T) {
	p, err := security.NewPII(encKey, hashKey)
	require.NoError(t, err)
	assert.Equal(t, p.HashNIK("3201234567890123"), p.HashNIK("3201234567890123"))
	assert.NotEqual(t, p.HashNIK("3201234567890123"), p.HashNIK("9999999999999999"))
}

func TestPII_EmailSealRoundtrip(t *testing.T) {
	p, err := security.NewPII(encKey, hashKey)
	require.NoError(t, err)
	enc, err := p.SealEmail("a@b.id")
	require.NoError(t, err)
	assert.NotContains(t, string(enc), "a@b.id")
	got, err := p.UnsealEmail(enc)
	require.NoError(t, err)
	assert.Equal(t, "a@b.id", got)
}

func newSigner(t *testing.T) (*token.Signer, *token.Verifier) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	signer, err := token.NewSigner(priv, "queue-1")
	require.NoError(t, err)
	verifier, err := token.NewVerifier(map[string]ed25519.PublicKey{"queue-1": pub}, "rbt-queue", "rbt", 5*time.Second)
	require.NoError(t, err)
	return signer, verifier
}

func TestTokenIssuer_AdmissionTTL(t *testing.T) {
	signer, verifier := newSigner(t)
	now := time.Now().UTC()
	iss := security.NewTokenIssuer(signer, "rbt-queue", "rbt", 60*time.Second, time.Hour, time.Hour)
	tok, exp, err := iss.IssueAdmission("e1", "ev1", "alloc-1", now)
	require.NoError(t, err)
	assert.WithinDuration(t, now.Add(60*time.Second), exp, time.Second)
	claims, err := verifier.Verify(tok, now)
	require.NoError(t, err)
	assert.Equal(t, "e1", claims.Subject)
	assert.Equal(t, security.ScopeAdmission, claims.Scope)
}

func TestTokenIssuer_VisitorAndQueue(t *testing.T) {
	signer, verifier := newSigner(t)
	now := time.Now().UTC()
	iss := security.NewTokenIssuer(signer, "rbt-queue", "rbt", time.Minute, time.Hour, time.Hour)

	vt, err := iss.IssueVisitor("v1", now)
	require.NoError(t, err)
	c, err := verifier.Verify(vt, now)
	require.NoError(t, err)
	assert.Equal(t, security.ScopeVisitor, c.Scope)

	qt, err := iss.IssueQueue("e1", "v1", now)
	require.NoError(t, err)
	c2, err := verifier.Verify(qt, now)
	require.NoError(t, err)
	assert.Equal(t, security.ScopeQueue, c2.Scope)
}

func TestUserTokenVerifier(t *testing.T) {
	signer, verifier := newSigner(t)
	now := time.Now().UTC()
	tok, err := signer.Sign(token.Claims{
		Issuer: "rbt-queue", Subject: "acc1", Audience: "rbt",
		IssuedAt: now.Unix(), ExpiresAt: now.Add(time.Hour).Unix(),
		HasNIK: true, NIKHash: "deadbeef", NIKMasked: "3174********0001", Email: "a@b.id",
	})
	require.NoError(t, err)

	uv := security.NewUserTokenVerifier(verifier, func() time.Time { return now })
	id, err := uv.Verify(tok)
	require.NoError(t, err)
	assert.Equal(t, "acc1", id.AccountID)
	assert.True(t, id.HasNIK)
	assert.Equal(t, "deadbeef", id.NIKHash)
	assert.Equal(t, "3174********0001", id.NIKMasked)
}

func TestVisitorAuthenticator_FromCookie(t *testing.T) {
	signer, verifier := newSigner(t)
	now := time.Now().UTC()
	iss := security.NewTokenIssuer(signer, "rbt-queue", "rbt", time.Minute, time.Hour, time.Hour)
	vt, err := iss.IssueVisitor("v-123", now)
	require.NoError(t, err)

	auth := security.NewVisitorAuthenticator(verifier, func() time.Time { return now })
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "rb_visitor", Value: vt})
	id, ok := auth.Resolve(req)
	assert.True(t, ok)
	assert.Equal(t, "v-123", id)
}

func TestVisitorAuthenticator_FromHeader(t *testing.T) {
	signer, verifier := newSigner(t)
	now := time.Now().UTC()
	iss := security.NewTokenIssuer(signer, "rbt-queue", "rbt", time.Minute, time.Hour, time.Hour)
	vt, _ := iss.IssueVisitor("v-456", now)

	auth := security.NewVisitorAuthenticator(verifier, func() time.Time { return now })
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Visitor-Token", vt)
	id, ok := auth.Resolve(req)
	assert.True(t, ok)
	assert.Equal(t, "v-456", id)
}

func TestVisitorAuthenticator_RejectsWrongScope(t *testing.T) {
	signer, verifier := newSigner(t)
	now := time.Now().UTC()
	iss := security.NewTokenIssuer(signer, "rbt-queue", "rbt", time.Minute, time.Hour, time.Hour)
	qt, _ := iss.IssueQueue("e1", "v1", now) // queue scope, not visitor

	auth := security.NewVisitorAuthenticator(verifier, func() time.Time { return now })
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Visitor-Token", qt)
	_, ok := auth.Resolve(req)
	assert.False(t, ok)
}

func TestVisitorAuthenticator_NoToken(t *testing.T) {
	_, verifier := newSigner(t)
	auth := security.NewVisitorAuthenticator(verifier, time.Now)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	_, ok := auth.Resolve(req)
	assert.False(t, ok)
}
