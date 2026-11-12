//go:build integration

package valkey

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pkgvalkey "github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/valkey"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/port/outbound"
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

func pttl(t *testing.T, c *pkgvalkey.Client, key string) int64 {
	t.Helper()
	n, err := c.VK().Do(context.Background(), c.VK().B().Pttl().Key(key).Build()).AsInt64()
	require.NoError(t, err)
	return n
}

func exists(t *testing.T, c *pkgvalkey.Client, key string) bool {
	t.Helper()
	n, err := c.VK().Do(context.Background(), c.VK().B().Exists().Key(key).Build()).AsInt64()
	require.NoError(t, err)
	return n > 0
}

// P-01: N concurrent correct verifications -> exactly one Matched, the rest NotFound (single use, invariant #5).
func TestOTPStore_Verify_SingleUseUnderConcurrency(t *testing.T) {
	ctx := context.Background()
	c := client(t)
	s := NewOTPStore(c.VK(), c)
	started, err := s.Start(ctx, "race", []byte{1, 2, 3}, time.Minute, time.Minute)
	require.NoError(t, err)
	require.True(t, started)

	const n = 20
	var matched, notFound, other atomic.Int32
	barrier := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-barrier
			res, err := s.Verify(ctx, "race", []byte{1, 2, 3}, 5)
			switch {
			case err != nil:
				other.Add(1)
			case res.Verdict == entity.OTPMatched:
				matched.Add(1)
			case res.Verdict == entity.OTPNotFound:
				notFound.Add(1)
			default:
				other.Add(1)
			}
		}()
	}
	close(barrier)
	wg.Wait()
	assert.EqualValues(t, 1, matched.Load(), "tepat satu pemenang")
	assert.EqualValues(t, n-1, notFound.Load())
	assert.EqualValues(t, 0, other.Load())
}

// P-02: Mismatch x max, then Locked (challenge deleted, cooldown kept); expired leaves no counter.
func TestOTPStore_Verify_MismatchThenLocked(t *testing.T) {
	ctx := context.Background()
	c := client(t)
	s := NewOTPStore(c.VK(), c)
	_, err := s.Start(ctx, "lock", []byte{1}, time.Minute, time.Minute)
	require.NoError(t, err)
	for i := 1; i <= 3; i++ {
		res, err := s.Verify(ctx, "lock", []byte{9}, 3)
		require.NoError(t, err)
		assert.Equal(t, entity.OTPMismatch, res.Verdict)
		assert.Equal(t, i, res.Attempts)
	}
	// percobaan ke-4 terkunci walau kodenya benar (sama seperti perilaku lama)
	res, err := s.Verify(ctx, "lock", []byte{1}, 3)
	require.NoError(t, err)
	assert.Equal(t, entity.OTPLocked, res.Verdict)
	code, attempts, cd := s.keys("lock")
	assert.False(t, exists(t, c, code))
	assert.True(t, exists(t, c, attempts), "counter tetap ada selama kode masih berlaku")
	assert.True(t, exists(t, c, cd), "cooldown tetap berlaku setelah lock")
	res, err = s.Verify(ctx, "lock", []byte{1}, 3)
	require.NoError(t, err)
	assert.Equal(t, entity.OTPLocked, res.Verdict, "terkunci sampai kode baru diminta, bukan NotFound")

	_, err = s.Start(ctx, "exp", []byte{1}, 150*time.Millisecond, time.Minute)
	require.NoError(t, err)
	time.Sleep(250 * time.Millisecond)
	res, err = s.Verify(ctx, "exp", []byte{1}, 3)
	require.NoError(t, err)
	assert.Equal(t, entity.OTPNotFound, res.Verdict)
	_, attempts, _ = s.keys("exp")
	assert.False(t, exists(t, c, attempts), "kedaluwarsa tidak meninggalkan counter")
}

// U5 (P3-16): a locked challenge keeps answering Locked, even for the right code, until a new code is requested.
func TestOTPVerify_LockedStaysLockedUntilNewCode(t *testing.T) {
	ctx := context.Background()
	c := client(t)
	s := NewOTPStore(c.VK(), c)
	_, err := s.Start(ctx, "stay", []byte{1}, time.Minute, 200*time.Millisecond)
	require.NoError(t, err)
	for i := 1; i <= 5; i++ {
		res, err := s.Verify(ctx, "stay", []byte{9}, 5)
		require.NoError(t, err)
		require.Equal(t, entity.OTPMismatch, res.Verdict)
	}
	res, err := s.Verify(ctx, "stay", []byte{9}, 5)
	require.NoError(t, err)
	assert.Equal(t, entity.OTPAttempt{Verdict: entity.OTPLocked, Attempts: 6}, res, "percobaan ke-6 terkunci")

	res, err = s.Verify(ctx, "stay", []byte{1}, 5)
	require.NoError(t, err)
	assert.Equal(t, entity.OTPLocked, res.Verdict, "kode benar tetap terkunci sampai kode baru diminta")

	time.Sleep(300 * time.Millisecond) // cooldown habis
	started, err := s.Start(ctx, "stay", []byte{2}, time.Minute, time.Second)
	require.NoError(t, err)
	require.True(t, started)
	res, err = s.Verify(ctx, "stay", []byte{2}, 5)
	require.NoError(t, err)
	assert.Equal(t, entity.OTPAttempt{Verdict: entity.OTPMatched, Attempts: 1}, res, "kode baru membuka kunci")
}

func TestOTPStore_Verify_MatchDeletesChallenge(t *testing.T) {
	ctx := context.Background()
	c := client(t)
	s := NewOTPStore(c.VK(), c)
	_, err := s.Start(ctx, "ok", []byte{7, 7}, time.Minute, time.Minute)
	require.NoError(t, err)
	res, err := s.Verify(ctx, "ok", []byte{7, 7}, 5)
	require.NoError(t, err)
	assert.Equal(t, entity.OTPAttempt{Verdict: entity.OTPMatched, Attempts: 1}, res)
	res, err = s.Verify(ctx, "ok", []byte{7, 7}, 5)
	require.NoError(t, err)
	assert.Equal(t, entity.OTPNotFound, res.Verdict, "replay setelah sukses")
}

// P-03: counter never outlives the code.
func TestOTPStore_Verify_CounterTTLBoundedByCode(t *testing.T) {
	ctx := context.Background()
	c := client(t)
	s := NewOTPStore(c.VK(), c)
	_, err := s.Start(ctx, "ttl", []byte{1}, 2*time.Second, time.Minute)
	require.NoError(t, err)
	_, err = s.Verify(ctx, "ttl", []byte{2}, 5)
	require.NoError(t, err)
	code, attempts, _ := s.keys("ttl")
	expireAt := func(k string) int64 {
		n, err := c.VK().Do(ctx, c.VK().B().Pexpiretime().Key(k).Build()).AsInt64()
		require.NoError(t, err)
		return n
	}
	assert.Greater(t, pttl(t, c, attempts), int64(0))
	assert.LessOrEqual(t, expireAt(attempts), expireAt(code), "counter tidak boleh hidup lebih lama dari kode")
}

// P-14: Start is atomic; Cancel clears all three keys; cooldown blocks a second Start.
func TestOTPStore_StartCancel(t *testing.T) {
	ctx := context.Background()
	c := client(t)
	s := NewOTPStore(c.VK(), c)
	in, err := s.InCooldown(ctx, "sc")
	require.NoError(t, err)
	assert.False(t, in)

	started, err := s.Start(ctx, "sc", []byte{1}, time.Minute, time.Minute)
	require.NoError(t, err)
	assert.True(t, started)
	code, attempts, cd := s.keys("sc")
	assert.True(t, exists(t, c, code))
	assert.True(t, exists(t, c, cd), "cooldown selalu ada bersama challenge")
	in, err = s.InCooldown(ctx, "sc")
	require.NoError(t, err)
	assert.True(t, in)

	_, _ = s.Verify(ctx, "sc", []byte{9}, 5) // buat counter
	again, err := s.Start(ctx, "sc", []byte{2}, time.Minute, time.Minute)
	require.NoError(t, err)
	assert.False(t, again)
	res, err := s.Verify(ctx, "sc", []byte{1}, 5)
	require.NoError(t, err)
	assert.Equal(t, entity.OTPMatched, res.Verdict, "Start yang ditolak tidak menimpa challenge")

	_, err = s.Start(ctx, "sc2", []byte{1}, time.Minute, time.Minute)
	require.NoError(t, err)
	_, _ = s.Verify(ctx, "sc2", []byte{9}, 5)
	require.NoError(t, s.Cancel(ctx, "sc2"))
	code, attempts, cd = s.keys("sc2")
	assert.False(t, exists(t, c, code))
	assert.False(t, exists(t, c, attempts))
	assert.False(t, exists(t, c, cd))

	_, err = s.Start(ctx, "sc3", []byte{1}, time.Minute, 200*time.Millisecond)
	require.NoError(t, err)
	time.Sleep(300 * time.Millisecond)
	started, err = s.Start(ctx, "sc3", []byte{4}, time.Minute, time.Second)
	require.NoError(t, err)
	assert.True(t, started, "setelah cooldown habis boleh minta lagi")
	res, err = s.Verify(ctx, "sc3", []byte{9}, 5)
	require.NoError(t, err)
	assert.Equal(t, 1, res.Attempts, "counter direset saat challenge baru")

	_, err = s.Start(ctx, "bad", []byte{1}, 0, time.Second)
	assert.Error(t, err)
}

type clk struct{ now time.Time }

func (c *clk) Now() time.Time { return c.now }

// P-15
func TestRateLimiter_AllowTTLAndWindow(t *testing.T) {
	ctx := context.Background()
	c := client(t)
	now := &clk{now: time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)}
	r := NewRateLimiter(c.VK(), c, now.Now)
	var last outbound.RateTicket
	for i := 1; i <= 3; i++ {
		tk, err := r.Allow(ctx, "otp:ip:1.2.3.4", 3, time.Hour)
		require.NoError(t, err)
		assert.True(t, tk.Allowed, "permintaan ke-%d", i)
		last = tk
	}
	first := pttl(t, c, last.Bucket)
	assert.Greater(t, first, int64(0), "key selalu punya TTL")
	tk, err := r.Allow(ctx, "otp:ip:1.2.3.4", 3, time.Hour)
	require.NoError(t, err)
	assert.False(t, tk.Allowed)
	assert.LessOrEqual(t, pttl(t, c, last.Bucket), first, "jendela tidak diperpanjang")

	now.now = now.now.Add(time.Hour)
	tk, err = r.Allow(ctx, "otp:ip:1.2.3.4", 3, time.Hour)
	require.NoError(t, err)
	assert.True(t, tk.Allowed, "jendela baru")
}

func TestRateLimiter_HealsKeyWithoutTTL(t *testing.T) {
	ctx := context.Background()
	c := client(t)
	r := NewRateLimiter(c.VK(), c, time.Now)
	tk, err := r.Allow(ctx, "heal", 5, time.Minute)
	require.NoError(t, err)
	require.NoError(t, c.VK().Do(ctx, c.VK().B().Persist().Key(tk.Bucket).Build()).Error())
	require.Equal(t, int64(-1), pttl(t, c, tk.Bucket))
	_, err = r.Allow(ctx, "heal", 5, time.Minute)
	require.NoError(t, err)
	assert.Greater(t, pttl(t, c, tk.Bucket), int64(0))
}

func TestRateLimiter_RejectsInvalidArgs(t *testing.T) {
	c := client(t)
	r := NewRateLimiter(c.VK(), c, time.Now)
	_, err := r.Allow(context.Background(), "x", 0, time.Minute)
	assert.Error(t, err)
	_, err = r.Allow(context.Background(), "x", 1, 0)
	assert.Error(t, err)
	_, err = r.Allow(context.Background(), "x", 1, time.Microsecond)
	assert.Error(t, err)
}

func counter(t *testing.T, c *pkgvalkey.Client, key string) int64 {
	t.Helper()
	n, err := c.VK().Do(context.Background(), c.VK().B().Get().Key(key).Build()).AsInt64()
	if err != nil {
		return 0
	}
	return n
}

func TestRateLimiter_Release(t *testing.T) {
	ctx := context.Background()
	c := client(t)
	now := &clk{now: time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)}
	r := NewRateLimiter(c.VK(), c, now.Now)

	// bucket hilang: tidak error, tidak membuat key
	missing := outbound.RateTicket{Bucket: c.Key("rl:{missing}:1")}
	require.NoError(t, r.Release(ctx, missing))
	assert.False(t, exists(t, c, missing.Bucket))
	require.NoError(t, r.Release(ctx, outbound.RateTicket{}))

	// positif: turun tepat 1
	t1, err := r.Allow(ctx, "rel", 5, time.Hour)
	require.NoError(t, err)
	_, _ = r.Allow(ctx, "rel", 5, time.Hour)
	require.EqualValues(t, 2, counter(t, c, t1.Bucket))
	require.NoError(t, r.Release(ctx, t1))
	assert.EqualValues(t, 1, counter(t, c, t1.Bucket))

	// nol: tetap 0
	require.NoError(t, r.Release(ctx, t1))
	require.NoError(t, r.Release(ctx, t1))
	assert.EqualValues(t, 0, counter(t, c, t1.Bucket))

	// batas jendela: tiket jendela lama hanya menurunkan jendela lama
	now.now = now.now.Add(time.Hour)
	for i := 0; i < 3; i++ {
		_, err = r.Allow(ctx, "rel", 5, time.Hour)
		require.NoError(t, err)
	}
	cur, err := r.Allow(ctx, "rel", 5, time.Hour)
	require.NoError(t, err)
	require.EqualValues(t, 4, counter(t, c, cur.Bucket))
	_, _ = r.Allow(ctx, "rel", 5, time.Hour) // 5
	old := outbound.RateTicket{Bucket: t1.Bucket}
	require.NoError(t, r.Release(ctx, old))
	assert.EqualValues(t, 5, counter(t, c, cur.Bucket), "jendela baru tidak tersentuh")

	// bucket kedaluwarsa: no-op
	short := &clk{now: time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)}
	rs := NewRateLimiter(c.VK(), c, short.Now)
	ts, err := rs.Allow(ctx, "exp", 5, 150*time.Millisecond)
	require.NoError(t, err)
	time.Sleep(250 * time.Millisecond)
	require.NoError(t, rs.Release(ctx, ts))
	assert.False(t, exists(t, c, ts.Bucket))
}
