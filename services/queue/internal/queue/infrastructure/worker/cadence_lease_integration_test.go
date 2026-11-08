//go:build integration

package worker_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"
)

// TC-QUE-33 (-race): a promoter ticking every 100 ms (per-job cadence) and a liveness sweep on a second client
// contend for the same per-event lease on the REAL store. Liveness must get through at least once (no starvation),
// every skipped pass is a silent no-op, and ZCARD active never exceeds capacity (invariant #2).
func TestPromoterCadenceAndLivenessContendForLease(t *testing.T) {
	const capacity = 3
	a, b := twoStores(t)
	ctx := context.Background()
	clk := &clock.Fixed{T: time.Now()}
	stub := newBookingStub(clk, 3*time.Minute, 1000)
	metrics := newFakeMetrics()
	settings := settingsFor(capacity)
	promoter := newIntegrationPromoter(a, stub, settings, metrics, clk)
	for i := 0; i < 8; i++ {
		seedWaiting(t, a, "w"+string(rune('a'+i)), i)
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	var maxActive atomic.Int64
	var runErr atomic.Value
	wg.Add(2)
	go func() { // promoter at the new 100 ms cadence
		defer wg.Done()
		tk := time.NewTicker(100 * time.Millisecond)
		defer tk.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tk.C:
				if err := promoter.Run(ctx); err != nil {
					runErr.Store(err)
				}
			}
		}
	}()
	go func() { // sampler
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				n, err := a.ActiveCount(ctx, "ev")
				if err != nil {
					runErr.Store(err)
					continue
				}
				for {
					cur := maxActive.Load()
					if int64(n) <= cur || maxActive.CompareAndSwap(cur, int64(n)) {
						break
					}
				}
			}
		}
	}()

	var passed, lost int
	for i := 0; i < 20; i++ {
		now := time.Now().UnixMilli()
		res, _, err := b.SweepLivenessTick(ctx, "ev", now-90_000, now-300_000, 10, now, 10*time.Second, 0)
		require.NoError(t, err)
		if res.LeaseLost {
			lost++
		} else {
			passed++
		}
		time.Sleep(50 * time.Millisecond)
	}
	close(stop)
	wg.Wait()

	if err := runErr.Load(); err != nil {
		t.Fatalf("worker error: %v", err)
	}
	assert.GreaterOrEqual(t, passed, 1, "liveness must not be starved (passed=%d lost=%d)", passed, lost)
	assert.LessOrEqual(t, maxActive.Load(), int64(capacity), "ZCARD active exceeded capacity")
	active, err := a.ActiveCount(ctx, "ev")
	require.NoError(t, err)
	assert.LessOrEqual(t, active, capacity)
	assert.Equal(t, capacity, active, "promoter filled the free slots")
}
