package security_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/token"

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
	v := security.NewBearerVerifier(nil, time.Now, security.ScopeService)
	_, err := v.VerifyAccess("any.token.here")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "belum dikonfigurasi")
}

// signer returns a verifier and a function that signs tokens with the given claims, like the Queue and User services do.
func signer(t *testing.T) (*token.Verifier, func(token.Claims) string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	sg, err := token.NewSigner(priv, "k1")
	require.NoError(t, err)
	v, err := token.NewVerifier(map[string]ed25519.PublicKey{"k1": pub}, "iss", "aud", 5*time.Second)
	require.NoError(t, err)
	return v, func(c token.Claims) string {
		now := time.Now()
		c.Issuer, c.Audience, c.IssuedAt, c.ExpiresAt = "iss", "aud", now.Unix(), now.Add(time.Minute).Unix()
		tok, err := sg.Sign(c)
		require.NoError(t, err)
		return tok
	}
}

// The Queue key signs visitor tokens too, and anyone who joins a queue gets one: a guard must check the scope.
func TestBearerVerifier_AcceptsOnlyItsScope(t *testing.T) {
	v, sign := signer(t)
	svc := security.NewBearerVerifier(v, time.Now, security.ScopeService)

	sub, err := svc.VerifyAccess(sign(token.Claims{Subject: "queue-service", Scope: "service"}))
	require.NoError(t, err)
	assert.Equal(t, "queue-service", sub)

	for _, scope := range []string{"visitor", "queue", "admission", "buyer", ""} {
		_, err := svc.VerifyAccess(sign(token.Claims{Subject: "x", Scope: scope}))
		assert.Error(t, err, "scope %q tidak boleh lolos sebagai service", scope)
	}
}

func TestAdminVerifier_OnlyAllowListedBuyerEmails(t *testing.T) {
	v, sign := signer(t)
	adm := security.NewAdminVerifier(v, time.Now, []string{" Admin@Contoh.id ", ""})

	_, err := adm.VerifyAccess(sign(token.Claims{Subject: "a1", Scope: "buyer", Email: "admin@contoh.id"}))
	assert.NoError(t, err, "email di allow-list (tanpa beda huruf besar)")
	_, err = adm.VerifyAccess(sign(token.Claims{Subject: "a2", Scope: "buyer", Email: "pembeli@contoh.id"}))
	assert.Error(t, err, "pembeli biasa bukan admin")
	_, err = adm.VerifyAccess(sign(token.Claims{Subject: "a3", Scope: "visitor", Email: "admin@contoh.id"}))
	assert.Error(t, err, "token visitor bukan token pembeli")
	_, err = adm.VerifyAccess(sign(token.Claims{Subject: "a4", Scope: "buyer"}))
	assert.Error(t, err, "tanpa email")
	dom := security.NewAdminVerifier(v, time.Now, []string{"@admin.e2e.test"})
	_, err = dom.VerifyAccess(sign(token.Claims{Subject: "d1", Scope: "buyer", Email: "x@admin.e2e.test"}))
	assert.NoError(t, err, "entri @domain")
	_, err = dom.VerifyAccess(sign(token.Claims{Subject: "d2", Scope: "buyer", Email: "x@evil.test"}))
	assert.Error(t, err)
	_, err = dom.VerifyAccess(sign(token.Claims{Subject: "d3", Scope: "buyer", Email: "x@sub.admin.e2e.test"}))
	assert.Error(t, err, "subdomain tidak ikut")
	_, err = security.NewAdminVerifier(v, time.Now, nil).VerifyAccess(sign(token.Claims{Subject: "a5", Scope: "buyer", Email: "admin@contoh.id"}))
	assert.Error(t, err, "allow-list kosong tidak mengizinkan siapa pun")
}

func TestAdmissionVerifier_OnlyAdmissionTokens(t *testing.T) {
	v, sign := signer(t)
	adm := security.NewAdmissionVerifier(v, time.Now)
	_, err := adm.Verify(sign(token.Claims{Subject: "entry", ID: "jti-1", Scope: "admission"}))
	assert.NoError(t, err)
	for _, scope := range []string{"visitor", "service", "queue", "buyer"} {
		_, err := adm.Verify(sign(token.Claims{Subject: "x", ID: "jti-2", Scope: scope}))
		assert.Error(t, err, scope)
	}
}
