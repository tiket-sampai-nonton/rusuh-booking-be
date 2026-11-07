//go:build integration

package valkey

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pkgvalkey "github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/valkey"
)

// REQ-QSH-13.1-.4: the limiter must never leave a counter key without a TTL, even when it races a
// previous window's expiry, and it must repair a key it finds with PTTL == -1 (legacy/crash state).
func TestRateLimiter_NoKeyIsEverLeftWithoutATTL(t *testing.T) {
	server := osEnv(t)
	c, err := pkgvalkey.New(context.Background(), pkgvalkey.Config{Server: server, PrefixEnv: fmt.Sprintf("rl%d", time.Now().UnixNano())})
	require.NoError(t, err)
	t.Cleanup(c.Close)
	rl := NewRateLimiter(c.VK(), c, time.Now)
	key := "it-ttl"

	for i := 0; i < 5; i++ {
		_, err := rl.Allow(context.Background(), key, 100, time.Minute)
		require.NoError(t, err)
		pttl, err := c.VK().Do(context.Background(), c.VK().B().Pttl().Key(c.Key("rl:{"+key+"}:"+bucketSuffix(time.Now(), time.Minute))).Build()).AsInt64()
		require.NoError(t, err)
		assert.Greater(t, pttl, int64(0), "setiap panggilan harus meninggalkan TTL positif")
	}
}

// A key created without a TTL (simulating a crash between INCR and PEXPIRE in the old implementation)
// must be repaired on the next call rather than becoming a permanent, unbounded counter.
func TestRateLimiter_RepairsAMissingTTL(t *testing.T) {
	server := osEnv(t)
	c, err := pkgvalkey.New(context.Background(), pkgvalkey.Config{Server: server, PrefixEnv: fmt.Sprintf("rl%d", time.Now().UnixNano())})
	require.NoError(t, err)
	t.Cleanup(c.Close)
	rl := NewRateLimiter(c.VK(), c, time.Now)
	key := "it-repair"
	rawKey := c.Key("rl:{" + key + "}:" + bucketSuffix(time.Now(), time.Minute))
	require.NoError(t, c.VK().Do(context.Background(), c.VK().B().Set().Key(rawKey).Value("1").Build()).Error())

	_, err = rl.Allow(context.Background(), key, 100, time.Minute)
	require.NoError(t, err)
	pttl, err := c.VK().Do(context.Background(), c.VK().B().Pttl().Key(rawKey).Build()).AsInt64()
	require.NoError(t, err)
	assert.Greater(t, pttl, int64(0), "PTTL -1 harus diperbaiki pada panggilan berikutnya")
}
