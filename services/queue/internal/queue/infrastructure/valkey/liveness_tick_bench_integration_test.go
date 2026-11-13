//go:build integration

package valkey

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"
	pkgvalkey "github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/valkey"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// BenchmarkLivenessTick_MassDisconnect times one liveness tick over 32k silent WAITING entries at B=16 with the default
// budget (QUEUE_LIVENESS_BATCH_MAX=1000). The entries were last heard 200 s ago: past StaleAfter (90 s), short of
// EndAfter (300 s). Threshold: <= 10 ms per call (PF-P2-1). The seeding joins are outside the timer.
// Run: go.sh test -tags integration -run '^$' -bench BenchmarkLivenessTick_MassDisconnect -benchtime=3x ./internal/queue/infrastructure/valkey/
func BenchmarkLivenessTick_MassDisconnect(b *testing.B) {
	const total, shards, budget = 32_000, 16, 1000
	server := os.Getenv("TEST_VALKEY_SERVER")
	if server == "" {
		b.Fatal("TEST_VALKEY_SERVER wajib diset untuk benchmark integrasi")
	}
	ctx := context.Background()
	now := time.Now().UnixMilli()
	silentSince := now - 200_000
	for n := 0; n < b.N; n++ {
		b.StopTimer()
		prefix := fmt.Sprintf("bench%d", time.Now().UnixNano()%1_000_000_000_000)
		c, err := pkgvalkey.New(ctx, pkgvalkey.Config{Server: server, PrefixEnv: prefix})
		require.NoError(b, err)
		s := NewQueueStore(c.VK(), c, clock.System{}, shards)
		ev := fmt.Sprintf("ev-mass-%d", n)
		for i := 1; i <= total; i++ {
			res, err := s.Join(ctx, outbound.JoinParams{
				EventID: ev, EntryID: fmt.Sprintf("entry-%d", i), NewVisitorID: fmt.Sprintf("visitor-%d", i),
				NowMs: silentSince, OneActivePerVisitor: true, TokenTTL: time.Hour,
			})
			require.NoError(b, err)
			require.Equal(b, outbound.JoinCreated, res.Outcome)
		}

		b.StartTimer()
		tick, _, err := s.SweepLivenessTick(ctx, ev, now-90_000, now-300_000, budget, now, 10*time.Second, 0, n)
		b.StopTimer()
		require.NoError(b, err)
		require.False(b, tick.LeaseLost)
		b.ReportMetric(float64(tick.Staled), "staled/op")
		c.Close()
	}
}
