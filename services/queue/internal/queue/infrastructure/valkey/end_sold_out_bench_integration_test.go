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

// BenchmarkSoldOutDrain_100k times the sold-out drain (D-C8.4) over 100k WAITING entries: EndWaitingSoldOut batches of
// outbound.SoldOutBatchSize until a short batch comes back, the same loop the sweeper runs within its tick budget.
// Threshold: the whole set ends within one sweeper tick (< 5 s). The joins that fill the set are outside the timer.
// Run: go.sh test -tags integration -run '^$' -bench BenchmarkSoldOutDrain_100k -benchtime=1x ./internal/queue/infrastructure/valkey/
func BenchmarkSoldOutDrain_100k(b *testing.B) {
	const total = 100_000
	server := os.Getenv("TEST_VALKEY_SERVER")
	if server == "" {
		b.Fatal("TEST_VALKEY_SERVER wajib diset untuk benchmark integrasi")
	}
	ctx := context.Background()
	for n := 0; n < b.N; n++ {
		b.StopTimer()
		prefix := fmt.Sprintf("bench%d", time.Now().UnixNano()%1_000_000_000_000)
		c, err := pkgvalkey.New(ctx, pkgvalkey.Config{Server: server, PrefixEnv: prefix})
		require.NoError(b, err)
		s := NewQueueStore(c.VK(), c, clock.System{}, defaultTestShards)
		ev := fmt.Sprintf("ev-bench-%d", n)
		now := time.Now().UnixMilli()
		for i := 1; i <= total; i++ {
			res, err := s.Join(ctx, outbound.JoinParams{
				EventID: ev, EntryID: fmt.Sprintf("entry-%d", i), NewVisitorID: fmt.Sprintf("visitor-%d", i),
				NowMs: now, OneActivePerVisitor: true,
			})
			require.NoError(b, err)
			require.Equal(b, outbound.JoinCreated, res.Outcome)
		}

		b.StartTimer()
		ended := 0
		for {
			res, err := s.EndWaitingSoldOut(ctx, ev, now, outbound.SoldOutBatchSize)
			require.NoError(b, err)
			ended += res.Ended
			if res.Scanned < outbound.SoldOutBatchSize {
				break
			}
		}
		b.StopTimer()
		require.Equal(b, total, ended, "every WAITING entry ends with SOLD_OUT")
		c.Close()
	}
}
