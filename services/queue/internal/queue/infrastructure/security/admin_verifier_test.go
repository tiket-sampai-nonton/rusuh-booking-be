package security_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/token"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/infrastructure/security"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

var _ outbound.IAdminTokenVerifier = (*security.AdminVerifier)(nil)

type adminCase struct {
	name    string
	claims  token.Claims
	emails  []string
	deny    *stubDenylist
	wantSub string
	wantErr bool
}

func TestAdminVerifier_VerifyAdmin(t *testing.T) {
	signer, verifier := newSigner(t)
	now := time.Now().UTC()
	mk := func(scope, sub, email, sid string, exp time.Duration) token.Claims {
		return token.Claims{
			Issuer: "rbt-queue", Audience: "rbt", Scope: scope, Subject: sub, Email: email, SessionID: sid,
			IssuedAt: now.Add(-2 * time.Hour).Unix(), ExpiresAt: now.Add(exp).Unix(),
		}
	}
	allow := []string{"admin@contoh.id", "@lokal.test"}
	cases := []adminCase{
		{name: "valid buyer in allow-list", claims: mk("buyer", "acct-1", "admin@contoh.id", "", time.Hour), emails: allow, wantSub: "acct-1"},
		{name: "case-insensitive email", claims: mk("buyer", "acct-1", "Admin@Contoh.ID", "", time.Hour), emails: allow, wantSub: "acct-1"},
		{name: "allow-list entry is case-insensitive too", claims: mk("buyer", "acct-1", "admin@contoh.id", "", time.Hour), emails: []string{" ADMIN@Contoh.id "}, wantSub: "acct-1"},
		{name: "domain entry", claims: mk("buyer", "acct-2", "siapa.saja@lokal.test", "", time.Hour), emails: allow, wantSub: "acct-2"},
		{name: "other domain rejected", claims: mk("buyer", "acct-2", "x@lain.test", "", time.Hour), emails: allow, wantErr: true},
		{name: "buyer not in allow-list", claims: mk("buyer", "acct-3", "orang@contoh.id", "", time.Hour), emails: allow, wantErr: true},
		{name: "empty allow-list admits nobody", claims: mk("buyer", "acct-1", "admin@contoh.id", "", time.Hour), emails: nil, wantErr: true},
		{name: "visitor scope rejected", claims: mk("visitor", "v1", "admin@contoh.id", "", time.Hour), emails: allow, wantErr: true},
		{name: "queue scope rejected", claims: mk("queue", "q1", "admin@contoh.id", "", time.Hour), emails: allow, wantErr: true},
		{name: "service scope rejected", claims: mk("service", "queue-service", "admin@contoh.id", "", time.Hour), emails: allow, wantErr: true},
		{name: "admission scope rejected", claims: mk("admission", "e1", "admin@contoh.id", "", time.Hour), emails: allow, wantErr: true},
		{name: "empty scope rejected", claims: mk("", "acct-1", "admin@contoh.id", "", time.Hour), emails: allow, wantErr: true},
		{name: "expired", claims: mk("buyer", "acct-1", "admin@contoh.id", "", -time.Hour), emails: allow, wantErr: true},
		{name: "empty subject rejected", claims: mk("buyer", "", "admin@contoh.id", "", time.Hour), emails: allow, wantErr: true},
		{name: "no email claim", claims: mk("buyer", "acct-1", "", "", time.Hour), emails: allow, wantErr: true},
		{name: "revoked session", claims: mk("buyer", "acct-1", "admin@contoh.id", "sess-1", time.Hour), emails: allow,
			deny: &stubDenylist{revoked: map[string]bool{"sess-1": true}}, wantErr: true},
		{name: "denylist unreadable fails closed", claims: mk("buyer", "acct-1", "admin@contoh.id", "sess-1", time.Hour), emails: allow,
			deny: &stubDenylist{err: errors.New("valkey mati")}, wantErr: true},
		{name: "other session passes", claims: mk("buyer", "acct-1", "admin@contoh.id", "sess-1", time.Hour), emails: allow,
			deny: &stubDenylist{revoked: map[string]bool{"sess-lain": true}}, wantSub: "acct-1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tok, err := signer.Sign(c.claims)
			require.NoError(t, err)
			v := security.NewAdminVerifier(verifier, func() time.Time { return now }, c.emails)
			if c.deny != nil {
				v = v.WithDenylist(*c.deny)
			}
			sub, err := v.VerifyAdmin(tok)
			if c.wantErr {
				require.Error(t, err)
				assert.Empty(t, sub)
				if c.claims.Email != "" {
					assert.NotContains(t, err.Error(), c.claims.Email, "error text never carries the email")
				}
				assert.NotContains(t, err.Error(), tok, "error text never carries the bearer")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, c.wantSub, sub)
		})
	}
}

func TestAdminVerifier_GarbageAndNil(t *testing.T) {
	_, verifier := newSigner(t)
	now := time.Now().UTC()
	v := security.NewAdminVerifier(verifier, func() time.Time { return now }, []string{"admin@contoh.id"})
	for _, bearer := range []string{"", "garbage", "a.b.c"} {
		_, err := v.VerifyAdmin(bearer)
		assert.Error(t, err, bearer)
	}
	var nilV *security.AdminVerifier
	_, err := nilV.VerifyAdmin("x")
	assert.Error(t, err, "nil verifier fails closed")
	_, err = security.NewAdminVerifier(nil, func() time.Time { return now }, []string{"a@b.c"}).VerifyAdmin("x")
	assert.Error(t, err, "verifier tanpa kunci gagal tertutup")
}
