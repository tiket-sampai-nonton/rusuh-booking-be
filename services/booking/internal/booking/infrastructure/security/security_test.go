package security_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/revocation"
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

func TestTicketLinks_BoundToTheBookingAndTheKey(t *testing.T) {
	links, err := security.NewTicketLinks([]byte("0123456789abcdef0123456789abcdef"))
	require.NoError(t, err)
	tok := links.Token("booking-1")
	assert.NotEmpty(t, tok)
	assert.True(t, links.Valid("booking-1", tok))
	assert.False(t, links.Valid("booking-2", tok), "tautan hanya membuka booking-nya sendiri")
	assert.False(t, links.Valid("booking-1", tok+"x"))
	assert.False(t, links.Valid("booking-1", ""))
	assert.Equal(t, tok, links.Token("booking-1"), "stabil")

	other, err := security.NewTicketLinks([]byte("ffffffffffffffffffffffffffffffff"))
	require.NoError(t, err)
	assert.False(t, other.Valid("booking-1", tok), "kunci lain tidak menerima")

	_, err = security.NewTicketLinks([]byte("pendek"))
	assert.Error(t, err)
}

type stubDenylist struct {
	revoked map[string]bool
	err     error
}

func (s stubDenylist) Revoke(context.Context, string, time.Duration) error { return nil }
func (s stubDenylist) IsRevoked(_ context.Context, sid string) (bool, error) {
	return s.revoked[sid], s.err
}

// SCRUM-53: an admin who logged out must not keep admin rights until exp; an unreadable denylist refuses everyone.
func TestAdminAndBuyerVerifiers_RefuseLoggedOutSessionsAndFailClosed(t *testing.T) {
	v, sign := signer(t)
	adminTok := sign(token.Claims{Subject: "a1", Scope: "buyer", Email: "admin@contoh.id", SessionID: "sess-1"})
	oldTok := sign(token.Claims{Subject: "a1", Scope: "buyer", Email: "admin@contoh.id"}) // tanpa sid (sebelum fitur ini)

	loggedOut := stubDenylist{revoked: map[string]bool{"sess-1": true}}
	adm := security.NewAdminVerifier(v, time.Now, []string{"admin@contoh.id"}).WithDenylist(loggedOut)
	_, err := adm.VerifyAccess(adminTok)
	assert.ErrorIs(t, err, revocation.ErrRevoked, "admin yang sudah logout ditolak")
	_, err = adm.VerifyAccess(oldTok)
	assert.NoError(t, err, "token lama tanpa sid tetap berlaku sampai exp")

	buyer := security.NewBearerVerifier(v, time.Now, security.ScopeBuyer).WithDenylist(loggedOut)
	_, err = buyer.VerifyAccess(sign(token.Claims{Subject: "b1", Scope: "buyer", SessionID: "sess-1"}))
	assert.ErrorIs(t, err, revocation.ErrRevoked)
	sub, err := buyer.VerifyAccess(sign(token.Claims{Subject: "b2", Scope: "buyer", SessionID: "sess-2"}))
	require.NoError(t, err)
	assert.Equal(t, "b2", sub, "sesi lain tidak terpengaruh")

	down := security.NewAdminVerifier(v, time.Now, []string{"admin@contoh.id"}).WithDenylist(stubDenylist{err: errors.New("valkey mati")})
	_, err = down.VerifyAccess(adminTok)
	assert.Error(t, err, "fail closed untuk admin bila denylist tidak bisa dibaca")

	none := security.NewAdminVerifier(v, time.Now, []string{"admin@contoh.id"}).WithDenylist(nil)
	_, err = none.VerifyAccess(adminTok)
	assert.NoError(t, err, "tanpa denylist (tidak dikonfigurasi) perilaku lama")
}
