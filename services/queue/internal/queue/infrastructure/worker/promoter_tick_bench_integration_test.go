//go:build integration

package worker_test

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	vk "github.com/valkey-io/valkey-go"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"
	pkgvalkey "github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/valkey"
	queuevalkey "github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/infrastructure/valkey"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/infrastructure/worker"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// countingVK counts the Valkey round trips the store makes: one per Do and one per DoMulti (a pipelined batch).
type countingVK struct {
	vk.Client
	n *atomic.Int64
}

func (c countingVK) Do(ctx context.Context, cmd vk.Completed) vk.ValkeyResult {
	c.n.Add(1)
	return c.Client.Do(ctx, cmd)
}

func (c countingVK) DoMulti(ctx context.Context, multi ...vk.Completed) []vk.ValkeyResult {
	c.n.Add(1)
	return c.Client.DoMulti(ctx, multi...)
}

// BenchmarkPromoterTick_200 times one promoter tick over 200 waiting candidates on the real Valkey (C8.8). The Booking
// stub has no stock, so every candidate answers NO_SUPPLY_NOW and the candidate set is identical on every tick. It also
// reports the Valkey round trips per tick.
func BenchmarkPromoterTick_200(b *testing.B) {
	server := os.Getenv("TEST_VALKEY_SERVER")
	if server == "" {
		b.Fatal("TEST_VALKEY_SERVER wajib diset untuk benchmark promoter (tanpa skip senyap)")
	}
	ctx := context.Background()
	prefix := fmt.Sprintf("bp8%d", time.Now().UnixNano()%1_000_000_000_000)
	c, err := pkgvalkey.New(ctx, pkgvalkey.Config{Server: server, PrefixEnv: prefix})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(c.Close)

	var trips atomic.Int64
	store := queuevalkey.NewQueueStore(countingVK{Client: c.VK(), n: &trips}, c, clock.System{}, 16)
	for i := 1; i <= 200; i++ {
		_, err := store.Join(ctx, outbound.JoinParams{
			EventID: "ev", EntryID: fmt.Sprintf("e%03d", i), NewVisitorID: fmt.Sprintf("v%03d", i),
			AccountID: fmt.Sprintf("a%03d", i), NowMs: time.Now().UnixMilli(), OneActivePerVisitor: true,
		})
		if err != nil {
			b.Fatal(err)
		}
	}
	clk := &clock.Fixed{T: time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)}
	stub := newBookingStub(clk, 3*time.Minute, 0)
	p := worker.NewPromoter(store, settingsFor(200), stub, &fakePub{}, itAudit{}, clk, nil, worker.PromoterTuning{BatchMax: 200})

	trips.Store(0)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := p.Run(ctx); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(trips.Load())/float64(b.N), "roundtrips/op")
}
