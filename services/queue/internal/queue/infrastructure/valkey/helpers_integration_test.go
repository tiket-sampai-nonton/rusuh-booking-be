//go:build integration

package valkey

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"
	pkgvalkey "github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/valkey"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// testClaimMaxMs is the production default claim bound (QUEUE_CLAIMED_SLOT_MAX_SECONDS=5700) in milliseconds.
const testClaimMaxMs int64 = 5_700_000

// osEnv fetches TEST_VALKEY_SERVER, failing (never skipping) when unset (plan R9-2 no-silent-skip).
func osEnv(t *testing.T) string {
	t.Helper()
	server := os.Getenv("TEST_VALKEY_SERVER")
	if server == "" {
		t.Fatal("TEST_VALKEY_SERVER wajib diset untuk test integrasi QSH (tanpa skip senyap)")
	}
	return server
}

// bucketSuffix mirrors RateLimiter's fixed-window bucket id so a test can read the exact key.
func bucketSuffix(now time.Time, window time.Duration) string {
	return strconv.FormatInt(now.UnixMilli()/window.Milliseconds(), 10)
}

// mustStore returns a QueueStore on the real Valkey named by TEST_VALKEY_SERVER. It never skips: an unset
// variable or an unreachable server fails the test (no silent skip, plan R9-2).
func mustStore(t *testing.T) *QueueStore {
	t.Helper()
	server := os.Getenv("TEST_VALKEY_SERVER")
	if server == "" {
		t.Fatal("TEST_VALKEY_SERVER wajib diset untuk test integrasi QSH (tanpa skip senyap)")
	}
	prefix := fmt.Sprintf("qsh%d", time.Now().UnixNano()%1_000_000_000)
	c, err := pkgvalkey.New(context.Background(), pkgvalkey.Config{Server: server, PrefixEnv: prefix})
	require.NoError(t, err)
	t.Cleanup(c.Close)
	return NewQueueStore(c.VK(), c, clock.System{}, defaultTestShards)
}

// evID returns an event id unique to the test run.
func evID(t *testing.T) string { return fmt.Sprintf("ev-%s-%d", t.Name(), time.Now().UnixNano()) }

// promoteBatch runs promote.lua under a real lease for the given candidates.
func promoteBatch(t *testing.T, s *QueueStore, ev string, cands []outbound.PromoteCandidate, capacity int, nowMs int64) outbound.PromoteResult {
	t.Helper()
	ctx := context.Background()
	tok, ok, err := s.AcquirePromoterLease(ctx, ev, 5*time.Second)
	require.NoError(t, err)
	require.True(t, ok)
	defer func() { require.NoError(t, s.ReleasePromoterLease(ctx, ev, tok)) }()
	res, err := s.Promote(ctx, ev, cands, capacity, nowMs, outbound.PromoteOpts{LeaseToken: tok})
	require.NoError(t, err)
	return res
}

// promoteOne admits one WAITING entry through promote.lua with a real lease token.
func promoteOne(t *testing.T, s *QueueStore, ev, id, alloc string, deadlineMs, nowMs int64, capacity int) outbound.PromoteResult {
	t.Helper()
	ctx := context.Background()
	tok, ok, err := s.AcquirePromoterLease(ctx, ev, 5*time.Second)
	require.NoError(t, err)
	require.True(t, ok, "lease harus bisa diambil di test")
	defer func() { require.NoError(t, s.ReleasePromoterLease(ctx, ev, tok)) }()
	res, err := s.Promote(ctx, ev, []outbound.PromoteCandidate{{EntryID: id, AllocationID: alloc, ClaimDeadlineMs: deadlineMs}}, capacity, nowMs, outbound.PromoteOpts{LeaseToken: tok})
	require.NoError(t, err)
	return res
}

func keyExists(t *testing.T, s *QueueStore, key string) bool {
	t.Helper()
	n, err := s.c.Do(context.Background(), s.c.B().Exists().Key(key).Build()).AsInt64()
	require.NoError(t, err)
	return n > 0
}

func hget(t *testing.T, s *QueueStore, key, field string) string {
	t.Helper()
	v, err := s.c.Do(context.Background(), s.c.B().Hget().Key(key).Field(field).Build()).ToString()
	if err != nil {
		return ""
	}
	return v
}

func zscore(t *testing.T, s *QueueStore, key, member string) (float64, bool) {
	t.Helper()
	v, err := s.c.Do(context.Background(), s.c.B().Zscore().Key(key).Member(member).Build()).AsFloat64()
	if err != nil {
		return 0, false
	}
	return v, true
}

func zcard(t *testing.T, s *QueueStore, key string) int64 {
	t.Helper()
	n, err := s.c.Do(context.Background(), s.c.B().Zcard().Key(key).Build()).AsInt64()
	require.NoError(t, err)
	return n
}

// legacyActive writes an entry the way the base (pre-QSH) scripts did, to pin self-healing on old data.
func zaddRaw(t *testing.T, s *QueueStore, key string, score float64, member string) {
	t.Helper()
	require.NoError(t, s.c.Do(context.Background(), s.c.B().Zadd().Key(key).ScoreMember().ScoreMember(score, member).Build()).Error())
}

func hsetRaw(t *testing.T, s *QueueStore, key string, kv ...string) {
	t.Helper()
	cmd := s.c.B().Hset().Key(key).FieldValue()
	for i := 0; i+1 < len(kv); i += 2 {
		cmd = cmd.FieldValue(kv[i], kv[i+1])
	}
	require.NoError(t, s.c.Do(context.Background(), cmd.Build()).Error())
}
