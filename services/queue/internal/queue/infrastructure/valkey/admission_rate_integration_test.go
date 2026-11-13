//go:build integration

package valkey

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// FR-09: promote.lua counts each admission in its epoch-minute counter (with a TTL), and only real promotions count.
func TestPromote_CountsAdmissionsPerMinute(t *testing.T) {
	s := mustStore(t)
	ctx := context.Background()
	ev := evID(t)
	now := time.Now().UnixMilli()
	minute := now / 60_000
	a, _, _ := joinOne(t, s, ev, 1, now)
	b, _, _ := joinOne(t, s, ev, 2, now)
	c, _, _ := joinOne(t, s, ev, 3, now)

	res := promoteBatch(t, s, ev, []outbound.PromoteCandidate{cand(a, now+60_000), cand(b, now+60_000)}, 5, now)
	require.Len(t, res.Promoted, 2)
	// Already admitted: skipped, so the counter does not move.
	res = promoteBatch(t, s, ev, []outbound.PromoteCandidate{cand(a, now+60_000)}, 5, now)
	require.Empty(t, res.Promoted)
	res = promoteBatch(t, s, ev, []outbound.PromoteCandidate{cand(c, now+60_000)}, 5, now)
	require.Len(t, res.Promoted, 1)

	got, err := s.AdmissionsPerMinute(ctx, ev, minute-2, minute)
	require.NoError(t, err)
	assert.Equal(t, []int64{0, 0, 3}, got, "missing minutes read as 0")

	pttl, err := s.c.Do(ctx, s.c.B().Pttl().Key(s.kAdm(ev, minute)).Build()).AsInt64()
	require.NoError(t, err)
	assert.Greater(t, pttl, int64(0))
	assert.LessOrEqual(t, pttl, int64(1_200_000))
}

// The capacity fields live in the sale hash next to SetSale/SetSaleWindow and do not clobber them.
func TestSaleCapacity_RoundTrip(t *testing.T) {
	s := mustStore(t)
	ctx := context.Background()
	ev := evID(t)
	require.NoError(t, s.SetSale(ctx, ev, "OPEN", false))
	require.NoError(t, s.SetSaleCapacity(ctx, ev, 3, 1_700_000_000_000))

	st, err := s.GetSale(ctx, ev)
	require.NoError(t, err)
	assert.Equal(t, "OPEN", st.SaleState)
	assert.Equal(t, 3, st.Capacity)
	assert.Equal(t, int64(1_700_000_000_000), st.CapacitySinceMs)
}
