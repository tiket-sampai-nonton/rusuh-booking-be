//go:build integration

package revocation_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/revocation"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/valkey"
)

func TestStore_RevokedSessionsExpireOnTheirOwn(t *testing.T) {
	server := os.Getenv("TEST_VALKEY_SERVER")
	if server == "" {
		t.Skip("TEST_VALKEY_SERVER tidak diset")
	}
	ctx := context.Background()
	c, err := valkey.New(ctx, valkey.Config{Server: server, PrefixEnv: "it"})
	require.NoError(t, err)
	defer c.Close()
	s := revocation.NewStore(c.VK(), c)

	revoked, err := s.IsRevoked(ctx, "sess-it-1")
	require.NoError(t, err)
	assert.False(t, revoked)

	require.NoError(t, s.Revoke(ctx, "sess-it-1", 400*time.Millisecond))
	revoked, err = s.IsRevoked(ctx, "sess-it-1")
	require.NoError(t, err)
	assert.True(t, revoked, "langsung berlaku setelah logout")

	time.Sleep(600 * time.Millisecond)
	revoked, err = s.IsRevoked(ctx, "sess-it-1")
	require.NoError(t, err)
	assert.False(t, revoked, "entri hilang sendiri setelah ttl: tidak menyimpan token yang sudah kedaluwarsa")

	require.NoError(t, s.Revoke(ctx, "", time.Minute), "sid kosong diabaikan")
	require.NoError(t, s.Revoke(ctx, "sess-it-2", 0), "ttl nol diabaikan")
	revoked, _ = s.IsRevoked(ctx, "sess-it-2")
	assert.False(t, revoked)
}
