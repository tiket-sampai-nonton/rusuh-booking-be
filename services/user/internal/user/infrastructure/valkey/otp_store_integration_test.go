//go:build integration

package valkey

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pkgvalkey "github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/valkey"
)

func client(t *testing.T) *pkgvalkey.Client {
	t.Helper()
	server := os.Getenv("TEST_VALKEY_SERVER")
	if server == "" {
		t.Skip("TEST_VALKEY_SERVER tidak diset")
	}
	c, err := pkgvalkey.New(context.Background(), pkgvalkey.Config{Server: server, PrefixEnv: "it" + time.Now().Format("150405.000")[7:]})
	require.NoError(t, err)
	t.Cleanup(c.Close)
	return c
}

func TestOTPStore_Lifecycle(t *testing.T) {
	ctx := context.Background()
	c := client(t)
	s := NewOTPStore(c.VK(), c, time.Minute)

	started, err := s.Start(ctx, "abc", []byte{1, 2, 3}, time.Minute, 300*time.Millisecond)
	require.NoError(t, err)
	assert.True(t, started)

	again, err := s.Start(ctx, "abc", []byte{9}, time.Minute, 300*time.Millisecond)
	require.NoError(t, err)
	assert.False(t, again, "cooldown masih aktif")

	ch, found, err := s.Attempt(ctx, "abc")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, []byte{1, 2, 3}, ch.CodeHash)
	assert.Equal(t, 1, ch.Attempts)
	ch, _, _ = s.Attempt(ctx, "abc")
	assert.Equal(t, 2, ch.Attempts)

	require.NoError(t, s.Clear(ctx, "abc"))
	_, found, err = s.Attempt(ctx, "abc")
	require.NoError(t, err)
	assert.False(t, found)

	time.Sleep(350 * time.Millisecond)
	started, err = s.Start(ctx, "abc", []byte{4}, time.Minute, time.Second)
	require.NoError(t, err)
	assert.True(t, started, "setelah cooldown habis boleh minta lagi")
	ch, _, _ = s.Attempt(ctx, "abc")
	assert.Equal(t, 1, ch.Attempts, "counter direset saat challenge baru")
}

func TestRateLimiter(t *testing.T) {
	ctx := context.Background()
	c := client(t)
	now := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	r := NewRateLimiter(c.VK(), c, func() time.Time { return now })
	for i := 1; i <= 3; i++ {
		ok, err := r.Allow(ctx, "otp:ip:1.2.3.4", 3, time.Hour)
		require.NoError(t, err)
		assert.True(t, ok, "permintaan ke-%d", i)
	}
	ok, err := r.Allow(ctx, "otp:ip:1.2.3.4", 3, time.Hour)
	require.NoError(t, err)
	assert.False(t, ok)

	now = now.Add(time.Hour)
	ok, err = r.Allow(ctx, "otp:ip:1.2.3.4", 3, time.Hour)
	require.NoError(t, err)
	assert.True(t, ok, "jendela baru")
}
