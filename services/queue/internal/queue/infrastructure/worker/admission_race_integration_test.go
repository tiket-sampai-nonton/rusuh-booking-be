//go:build integration

package worker_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"
	queuevalkey "github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/infrastructure/valkey"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

func seedWaiting(t *testing.T, s *queuevalkey.QueueStore, id string, n int) {
	t.Helper()
	res, err := s.Join(context.Background(), outbound.JoinParams{
		EventID: "ev", EntryID: id, VisitorID: "v-" + id, NikHash: fmt.Sprintf("%064x", 7000+n), EmailHashHex: fmt.Sprintf("%064x", 8000+n),
		EmailEnc: []byte("sealed"), NikMasked: "3201********0001", NowMs: time.Now().UnixMilli(), OneActivePerVisitor: true, OneActivePerEmail: true,
	})
	require.NoError(t, err)
	require.Equal(t, outbound.JoinOK, res.Outcome)
}

// TC-QUE-19c-INT (-race): joiners and the promoter loop race for a small capacity on the REAL store and real Lua.
// Scope (R9-1): this proves the Go callers and promote.lua's capacity under real concurrency. It is NOT sensitive to
// the fence (NX alone gives exclusion while the lease is never lost here); the fence is killed by TestAdmissionStale.
func TestAdmissionRace(t *testing.T) {
	const capacity, joiners, iterations = 3, 8, 6
	for it := 0; it < iterations; it++ {
		a, _ := twoStores(t)
		ctx := context.Background()
		clk := &clock.Fixed{T: time.Now()}
		stub := newBookingStub(clk, 3*time.Minute, 1000)
		metrics := newFakeMetrics()
		settings := settingsFor(capacity)
		uc := newJoinUC(a, stub, settings, metrics)
		promoter := newIntegrationPromoter(a, stub, settings, metrics, clk)

		stop := make(chan struct{})
		var wg sync.WaitGroup
		var maxActive atomic.Int64
		var sampleErr atomic.Value
		wg.Add(2)
		go func() { // promoter loop
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					if err := promoter.Run(ctx); err != nil {
						sampleErr.Store(err)
					}
				}
			}
		}()
		go func() { // sampler: ZCARD active must never exceed capacity
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					n, err := a.ActiveCount(ctx, "ev")
					if err != nil {
						sampleErr.Store(err)
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
		var jw sync.WaitGroup
		for n := 1; n <= joiners; n++ {
			jw.Add(1)
			go func(n int) {
				defer jw.Done()
				if _, err := joinAs(ctx, uc, n); err != nil {
					sampleErr.Store(err)
				}
			}(n)
		}
		jw.Wait()
		time.Sleep(50 * time.Millisecond) // let the promoter settle
		close(stop)
		wg.Wait()
		if v := sampleErr.Load(); v != nil {
			t.Fatalf("iterasi %d: galat tak terduga: %v", it, v)
		}

		assert.LessOrEqual(t, maxActive.Load(), int64(capacity), "iterasi %d: ZCARD active melebihi kapasitas (INV-1)", it)
		assert.Zero(t, metrics.Count(outbound.MetricFullAfterGrant), "iterasi %d: FULL setelah grant tidak boleh terjadi di bawah lease", it)
		// Quiescence: every RESERVED row belongs to an ADMITTED entry, never to a WAITING one (INV-2 / T1).
		stub.mu.Lock()
		rows := map[string]string{}
		for id, row := range stub.rows {
			rows[id] = row.status
		}
		stub.mu.Unlock()
		admitted := 0
		for id, st := range rows {
			status := realStatus(t, a, id)
			if st == "RESERVED" {
				assert.Equal(t, "ADMITTED", status, "iterasi %d: baris RESERVED untuk entry %s yang berstatus %s", it, id, status)
			}
			assert.NotEqual(t, "RELEASED", st, "iterasi %d: baris dilepas untuk entry yang masih hidup (%s)", it, id)
			if status == "ADMITTED" {
				admitted++
			}
		}
		assert.Equal(t, capacity, admitted, "iterasi %d: kapasitas terisi penuh", it)
	}
}

// detStore runs hook exactly once, immediately BEFORE delegating the first AcquirePromoterLease. It is a test
// decorator (no production seam) that models a competitor winning the last slot after the caller's lock-free
// pre-check but before the caller holds the lease.
type detStore struct {
	outbound.IQueueStore
	hook func()
	done bool
}

func (d *detStore) AcquirePromoterLease(ctx context.Context, ev string, ttl time.Duration) (string, bool, error) {
	if !d.done {
		d.done = true
		d.hook()
	}
	return d.IQueueStore.AcquirePromoterLease(ctx, ev, ttl)
}

// TC-QUE-19c-DET: deterministic, scheduler-independent. Join A passes its pre-check, a competitor takes the last
// slot through a real fenced Promote, then A obtains the lease: only the under-lease re-check can stop A, and it must
// do so BEFORE any Booking call.
func TestAdmissionDet(t *testing.T) {
	a, b := twoStores(t)
	ctx := context.Background()
	clk := &clock.Fixed{T: time.Now()}
	stub := newBookingStub(clk, 3*time.Minute, 100)
	metrics := newFakeMetrics()
	settings := settingsFor(1)

	det := &detStore{IQueueStore: a}
	det.hook = func() {
		seedWaiting(t, b, "competitor", 900)
		tokB, ok, err := b.AcquirePromoterLease(ctx, "ev", 5*time.Second)
		require.NoError(t, err)
		require.True(t, ok)
		res, err := b.Promote(ctx, "ev", []outbound.PromoteCandidate{{EntryID: "competitor", AllocationID: "alloc-comp", ClaimDeadlineMs: time.Now().Add(time.Minute).UnixMilli()}}, 1, time.Now().UnixMilli(), outbound.PromoteOpts{LeaseToken: tokB})
		require.NoError(t, err)
		require.Equal(t, []string{"competitor"}, res.Promoted)
		require.NoError(t, b.ReleasePromoterLease(ctx, "ev", tokB))
	}
	uc := newJoinUC(det, stub, settings, metrics)

	resp, err := joinAs(ctx, uc, 1)
	require.NoError(t, err)
	assert.Equal(t, "WAITING", resp.Status)
	reserve, release := stub.Calls()
	assert.Zero(t, reserve, "A tidak boleh memanggil Booking setelah re-check di bawah lease gagal")
	assert.Zero(t, release)
	n, err := a.ActiveCount(ctx, "ev")
	require.NoError(t, err)
	assert.Equal(t, 1, n, "ZCARD active == kapasitas")
	stub.mu.Lock()
	_, hasRow := stub.rows[resp.EntryID]
	stub.mu.Unlock()
	assert.False(t, hasRow, "tidak ada baris Booking untuk A (tidak ada yang bisa menua menjadi ALREADY_ACTIVE)")
}
