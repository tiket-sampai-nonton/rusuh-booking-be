package revocation_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/revocation"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/token"
)

type fakeDenylist struct {
	revoked map[string]bool
	err     error
}

func (f *fakeDenylist) Revoke(_ context.Context, sid string, _ time.Duration) error {
	f.revoked[sid] = true
	return nil
}

func (f *fakeDenylist) IsRevoked(_ context.Context, sid string) (bool, error) {
	return f.revoked[sid], f.err
}

func TestCheck_RevokedSessionIsRefused(t *testing.T) {
	d := &fakeDenylist{revoked: map[string]bool{"sess-1": true}}
	assert.ErrorIs(t, revocation.Check(d, token.Claims{SessionID: "sess-1"}), revocation.ErrRevoked)
	assert.NoError(t, revocation.Check(d, token.Claims{SessionID: "sess-2"}), "sesi lain tidak terpengaruh")
}

func TestCheck_TokensWithoutSessionOrWithoutDenylistAreAccepted(t *testing.T) {
	d := &fakeDenylist{revoked: map[string]bool{"": true}}
	assert.NoError(t, revocation.Check(d, token.Claims{}), "token lama tanpa sid tetap berlaku sampai exp")
	assert.NoError(t, revocation.Check(nil, token.Claims{SessionID: "sess-1"}), "denylist belum dikonfigurasi")
}

func TestCheck_FailsClosedWhenTheDenylistCannotBeRead(t *testing.T) {
	boom := errors.New("valkey mati")
	d := &fakeDenylist{revoked: map[string]bool{}, err: boom}
	assert.ErrorIs(t, revocation.Check(d, token.Claims{SessionID: "sess-1"}), boom, "tidak boleh lolos hanya karena Valkey bermasalah")
}
