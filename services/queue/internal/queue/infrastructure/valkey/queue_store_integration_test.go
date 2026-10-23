//go:build integration

package valkey

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"
	pkgvalkey "github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/valkey"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

func newTestStore(t *testing.T) *QueueStore {
	t.Helper()
	server := os.Getenv("TEST_VALKEY_SERVER")
	if server == "" {
		t.Skip("TEST_VALKEY_SERVER tidak diset")
	}
	// Use a unique prefix per test run to avoid cross-test pollution.
	prefix := "it" + time.Now().Format("150405.000")[7:]
	c, err := pkgvalkey.New(context.Background(), pkgvalkey.Config{Server: server, PrefixEnv: prefix})
	require.NoError(t, err)
	t.Cleanup(c.Close)
	return NewQueueStore(c.VK(), c, clock.System{})
}

// TestQueueStore_JoinGaplessSeq proves that join.lua maintains a gapless monotonic seq
// under 10,000 concurrent join goroutines (TC-QUE-08, spec 4.1, req 2.6/2.14, Property 1).
func TestQueueStore_JoinGaplessSeq(t *testing.T) {
	const n = 10_000
	store := newTestStore(t)
	ctx := context.Background()
	eventID := "evt-integration-seq"

	seqs := make(chan int64, n)
	var wg sync.WaitGroup
	wg.Add(n)

	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			entryID := fmt.Sprintf("entry-%d", i)
			visitorID := fmt.Sprintf("visitor-%d", i)
			nikHash := fmt.Sprintf("%064x", i) // unique 64-char hex per goroutine

			res, err := store.Join(ctx, outbound.JoinParams{
				EventID:   eventID,
				EntryID:   entryID,
				VisitorID: visitorID,
				NikHash:   nikHash,
				// EmailHashHex uniqueness prevents EMAIL_ALREADY_IN_QUEUE.
				EmailHashHex: fmt.Sprintf("%064x", i+n),
				NowMs:        time.Now().UnixMilli(),
			})
			if err != nil {
				return // counted as missing seq; test will fail below
			}
			if res.Outcome == outbound.JoinOK {
				seqs <- res.Seq
			}
		}(i)
	}

	wg.Wait()
	close(seqs)

	collected := make(map[int64]bool)
	for s := range seqs {
		assert.False(t, collected[s], "duplicate seq: %d", s)
		collected[s] = true
	}

	require.Equal(t, n, len(collected), "expected %d unique seq values, got %d", n, len(collected))
	for i := int64(1); i <= n; i++ {
		assert.True(t, collected[i], "seq %d missing (gapless check failed)", i)
	}
}

// TestQueueStore_JoinNIKConstraint proves that two joins with the same NIK hash return exactly
// one success and one JoinExistingNIK outcome (Property 4: one active entry per NIK).
func TestQueueStore_JoinNIKConstraint(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	eventID := "evt-integration-nik"
	nikHash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" // 64 hex chars

	res1, err := store.Join(ctx, outbound.JoinParams{
		EventID: eventID, EntryID: "e1", VisitorID: "v1", NikHash: nikHash,
		EmailHashHex: "b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1",
		NowMs:        time.Now().UnixMilli(),
	})
	require.NoError(t, err)
	assert.Equal(t, outbound.JoinOK, res1.Outcome, "first join should succeed")

	res2, err := store.Join(ctx, outbound.JoinParams{
		EventID: eventID, EntryID: "e2", VisitorID: "v2", NikHash: nikHash,
		EmailHashHex: "c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2",
		NowMs:        time.Now().UnixMilli(),
	})
	require.NoError(t, err)
	assert.Equal(t, outbound.JoinExistingNIK, res2.Outcome, "second join with same NIK should be rejected")
}
