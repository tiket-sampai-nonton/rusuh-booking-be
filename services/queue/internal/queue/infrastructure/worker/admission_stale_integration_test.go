//go:build integration

package worker_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"
	queuevalkey "github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/infrastructure/valkey"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// staleOnPromote wraps the real store; every method delegates unchanged except Promote. On the FIRST Promote call it
// (1) releases the caller's lease A through the real owner-safe release (models the TTL lapsing), (2) lets a second
// store acquire the lease as B, (3) only with withCompetitor, joins a competitor and admits it into the last slot with
// a real fenced Promote(token=B), and (4) delegates the caller's Promote (still token A) to the real store, recording
// the returned PromoteResult.
type staleOnPromote struct {
	outbound.IQueueStore
	t              *testing.T
	b              *queuevalkey.QueueStore
	withCompetitor bool
	done           bool
	bToken         string
	last           outbound.PromoteResult
}

func (s *staleOnPromote) Promote(ctx context.Context, ev string, cands []outbound.PromoteCandidate, capacity int, nowMs int64, opts outbound.PromoteOpts) (outbound.PromoteResult, error) {
	if !s.done {
		s.done = true
		require.NoError(s.t, s.IQueueStore.ReleasePromoterLease(ctx, ev, opts.LeaseToken))
		tok, ok, err := s.b.AcquirePromoterLease(ctx, ev, 10*time.Second)
		require.NoError(s.t, err)
		require.True(s.t, ok, "B harus bisa mengambil lease yang baru dilepas")
		s.bToken = tok
		if s.withCompetitor {
			seedWaiting(s.t, s.b, "competitor-stale", 950)
			res, err := s.b.Promote(ctx, ev, []outbound.PromoteCandidate{{EntryID: "competitor-stale", AllocationID: "alloc-comp-stale", ClaimDeadlineMs: time.Now().Add(time.Minute).UnixMilli()}}, capacity, nowMs, outbound.PromoteOpts{LeaseToken: tok})
			require.NoError(s.t, err)
			require.Equal(s.t, []string{"competitor-stale"}, res.Promoted)
		}
	}
	res, err := s.IQueueStore.Promote(ctx, ev, cands, capacity, nowMs, opts)
	s.last = res
	return res, err
}

// TC-QUE-19c-STALE (R9-1): the fence's deterministic concurrency proof. A stale lease token must never admit through
// promote.lua, on both the promoter and the join paths, with and without a competitor filling the last slot.
func TestAdmissionStale(t *testing.T) {
	run := func(t *testing.T, withCompetitor, viaPromoter bool) {
		a, b := twoStores(t)
		ctx := context.Background()
		clk := &clock.Fixed{T: time.Now()}
		stub := newBookingStub(clk, 3*time.Minute, 10)
		metrics := newFakeMetrics()
		settings := settingsFor(1)
		stale := &staleOnPromote{IQueueStore: a, t: t, b: b, withCompetitor: withCompetitor}

		var entryID string
		if viaPromoter {
			entryID = "A"
			seedWaiting(t, a, entryID, 1) // a WAITING head for the promoter to reserve
			p := newIntegrationPromoter(stale, stub, settings, metrics, clk)
			require.NoError(t, p.Run(ctx))
		} else {
			uc := newJoinUC(stale, stub, settings, metrics)
			resp, err := joinAs(ctx, uc, 1) // the direct-admission path of the join itself
			require.NoError(t, err)
			assert.Equal(t, "WAITING", resp.Status)
			entryID = resp.EntryID
		}

		assert.True(t, stale.last.LeaseLost, "token basi harus LEASE_LOST")
		assert.False(t, stale.last.Full, "bukan FULL: fence harus ditolak lebih dulu")
		assert.Empty(t, stale.last.Promoted)
		assert.Equal(t, "WAITING", realStatus(t, a, entryID))
		top, err := a.TopWaiting(ctx, "ev", 10)
		require.NoError(t, err)
		assert.Contains(t, top, entryID, "tetap di waiting")
		assert.Equal(t, 1, metrics.Count(outbound.MetricLeaseLost))
		assert.Zero(t, metrics.Count(outbound.MetricFullAfterGrant))

		stub.mu.Lock()
		row, hasRow := stub.rows[entryID]
		stub.mu.Unlock()
		require.True(t, hasRow, "baris RESERVED untuk A tetap ada (tidak dilepas pada LeaseLost)")
		assert.Equal(t, "RESERVED", row.status)
		assert.Empty(t, stub.ReleasedIDs())

		n, err := a.ActiveCount(ctx, "ev")
		require.NoError(t, err)
		if withCompetitor {
			assert.Equal(t, 1, n)
			assert.Equal(t, "ADMITTED", realStatus(t, a, "competitor-stale"))
		} else {
			assert.Equal(t, 0, n)
		}

		// The caller's deferred release used the stale token and must not have deleted B's lease.
		_, ok, err := a.AcquirePromoterLease(ctx, "ev", 5*time.Second)
		require.NoError(t, err)
		assert.False(t, ok, "pelepasan token basi tidak boleh menghapus lease B (P10)")

		if viaPromoter && !withCompetitor {
			// The stale case leaves no dead row: once B is gone, the next tick admits A with the SAME allocation id.
			require.NoError(t, b.ReleasePromoterLease(ctx, "ev", stale.bToken))
			p := newIntegrationPromoter(a, stub, settings, metrics, clk)
			require.NoError(t, p.Run(ctx))
			e, _, err := a.GetEntry(ctx, "ev", entryID)
			require.NoError(t, err)
			assert.Equal(t, "ADMITTED", string(e.Status))
			assert.Equal(t, row.allocID, e.AllocationID, "replay: sama persis dengan alokasi yang sudah ada")
			assert.Empty(t, stub.ReleasedIDs())
		}
	}

	t.Run("promoter_no_competitor", func(t *testing.T) { run(t, false, true) })
	t.Run("promoter_competitor_fills_slot", func(t *testing.T) { run(t, true, true) })
	t.Run("join_no_competitor", func(t *testing.T) { run(t, false, false) })
	t.Run("join_competitor_fills_slot", func(t *testing.T) { run(t, true, false) })
}
