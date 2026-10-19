package resilience

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time      { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

var errDown = errors.New("booking down")

func newBreaker(t *testing.T, clk *clock, changes *[]string) *Breaker {
	t.Helper()
	b, err := NewBreaker(BreakerConfig{Name: "queue→booking", FailureThreshold: 3, Cooldown: 5 * time.Second, HalfOpenProbes: 1,
		OnStateChange: func(_ string, from, to State) { *changes = append(*changes, from.String()+"→"+to.String()) }}, clk.now)
	require.NoError(t, err)
	return b
}

func fail(context.Context) error    { return errDown }
func success(context.Context) error { return nil }

func TestBreaker_OpensAfterThresholdAndRecovers(t *testing.T) {
	clk := &clock{t: time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)}
	var changes []string
	b := newBreaker(t, clk, &changes)
	ctx := context.Background()

	assert.ErrorIs(t, b.Do(ctx, fail), errDown)
	assert.NoError(t, b.Do(ctx, success), "sukses mereset hitungan")
	for i := 0; i < 3; i++ {
		assert.ErrorIs(t, b.Do(ctx, fail), errDown)
	}
	assert.Equal(t, Open, b.State())

	var called atomic.Bool
	err := b.Do(ctx, func(context.Context) error { called.Store(true); return nil })
	assert.ErrorIs(t, err, ErrOpen)
	assert.False(t, called.Load(), "dependency tidak dipanggil saat OPEN")

	clk.add(5 * time.Second)
	assert.Equal(t, HalfOpen, b.State())
	assert.ErrorIs(t, b.Do(ctx, fail), errDown, "probe gagal → OPEN lagi")
	assert.Equal(t, Open, b.State())

	clk.add(5 * time.Second)
	assert.NoError(t, b.Do(ctx, success), "probe sukses → CLOSED")
	assert.Equal(t, Closed, b.State())
	assert.Equal(t, []string{"closed→open", "open→half_open", "half_open→open", "open→half_open", "half_open→closed"}, changes)
}

func TestBreaker_HalfOpenAllowsLimitedProbes(t *testing.T) {
	clk := &clock{t: time.Now()}
	var changes []string
	b := newBreaker(t, clk, &changes)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		_ = b.Do(ctx, fail)
	}
	clk.add(6 * time.Second)

	started := make(chan struct{})
	release := make(chan struct{})
	go func() {
		_ = b.Do(ctx, func(context.Context) error { close(started); <-release; return nil })
	}()
	<-started
	assert.ErrorIs(t, b.Do(ctx, success), ErrOpen, "probe kedua ditolak selama probe pertama berjalan")
	close(release)
	assert.Eventually(t, func() bool { return b.State() == Closed }, time.Second, time.Millisecond)
}

func TestBreaker_TimeoutAndCanceledIsNotFailure(t *testing.T) {
	b, err := NewBreaker(BreakerConfig{Name: "smtp", FailureThreshold: 1, Cooldown: time.Second, HalfOpenProbes: 1, Timeout: 10 * time.Millisecond}, nil)
	require.NoError(t, err)
	err = b.Do(context.Background(), func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Equal(t, Open, b.State(), "timeout dihitung gagal")

	b2, _ := NewBreaker(BreakerConfig{Name: "x", FailureThreshold: 1, Cooldown: time.Second, HalfOpenProbes: 1}, nil)
	_ = b2.Do(context.Background(), func(context.Context) error { return context.Canceled })
	assert.Equal(t, Closed, b2.State(), "pembatalan oleh pemanggil bukan kegagalan dependency")
}

func TestNewBreaker_Validates(t *testing.T) {
	_, err := NewBreaker(BreakerConfig{Name: "x"}, nil)
	assert.Error(t, err)
}

func TestBulkhead(t *testing.T) {
	bh, err := NewBulkhead(2, 0)
	require.NoError(t, err)
	ctx := context.Background()
	hold := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		started := make(chan struct{})
		go func() {
			defer wg.Done()
			_ = bh.Do(ctx, func(context.Context) error { close(started); <-hold; return nil })
		}()
		<-started
	}
	assert.ErrorIs(t, bh.Do(ctx, success), ErrBulkheadFull)

	waiting, _ := NewBulkhead(1, 20*time.Millisecond)
	inner := make(chan struct{})
	go func() { _ = waiting.Do(ctx, func(context.Context) error { <-inner; return nil }) }()
	time.Sleep(5 * time.Millisecond)
	assert.ErrorIs(t, waiting.Do(ctx, success), ErrBulkheadFull, "menunggu lalu menyerah")
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	assert.ErrorIs(t, waiting.Do(cctx, success), context.Canceled)
	close(inner)
	assert.Eventually(t, func() bool { return waiting.Do(ctx, success) == nil }, time.Second, time.Millisecond)

	close(hold)
	wg.Wait()
	assert.NoError(t, bh.Do(ctx, success))
	_, err = NewBulkhead(0, 0)
	assert.Error(t, err)
}
