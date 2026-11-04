//go:build integration

package valkey

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Two acquirers never both hold the lease (owner-safe mutual exclusion, design §5.5, P10).
func TestLease_TwoAcquirersNeverBothHoldIt(t *testing.T) {
	s := mustStore(t)
	ctx := context.Background()
	ev := evID(t)
	_, okA, err := s.AcquirePromoterLease(ctx, ev, 5*time.Second)
	require.NoError(t, err)
	require.True(t, okA)
	_, okB, err := s.AcquirePromoterLease(ctx, ev, 5*time.Second)
	require.NoError(t, err)
	assert.False(t, okB)
}

// A stale-token release (after the holder's TTL already expired and someone else re-acquired) must not delete
// the new holder's lease: release is compare-and-delete by token, not a blind DEL (fence half of ADR-028).
func TestLease_StaleTokenReleaseDoesNotDeleteTheNewHoldersLease(t *testing.T) {
	s := mustStore(t)
	ctx := context.Background()
	ev := evID(t)
	tokA, ok, err := s.AcquirePromoterLease(ctx, ev, 50*time.Millisecond)
	require.NoError(t, err)
	require.True(t, ok)
	time.Sleep(80 * time.Millisecond) // let A's TTL lapse
	tokB, ok, err := s.AcquirePromoterLease(ctx, ev, 5*time.Second)
	require.NoError(t, err)
	require.True(t, ok)
	require.NotEqual(t, tokA, tokB, "token harus unik per pengambilan (owner-safe)")

	require.NoError(t, s.ReleasePromoterLease(ctx, ev, tokA)) // stale release, must be a no-op

	_, okC, err := s.AcquirePromoterLease(ctx, ev, 5*time.Second)
	require.NoError(t, err)
	assert.False(t, okC, "lease B harus tetap berdiri setelah pelepasan token basi")
	require.NoError(t, s.ReleasePromoterLease(ctx, ev, tokB))
}

// The real holder's release frees the lease for the next acquirer.
func TestLease_HoldersReleaseFreesIt(t *testing.T) {
	s := mustStore(t)
	ctx := context.Background()
	ev := evID(t)
	tok, ok, err := s.AcquirePromoterLease(ctx, ev, 5*time.Second)
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, s.ReleasePromoterLease(ctx, ev, tok))
	_, ok, err = s.AcquirePromoterLease(ctx, ev, 5*time.Second)
	require.NoError(t, err)
	assert.True(t, ok)
}
