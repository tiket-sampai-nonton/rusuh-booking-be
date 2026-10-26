//go:build integration

package outbox_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/outbox"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/postgres"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/rabbitmq"
)

const aggregateID = "11111111-1111-1111-1111-111111111111"

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// setup membuat schema it_outbox dengan tabel yang sama seperti migrasi 0001 Payment/Booking.
func setup(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN tidak diset")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	_, err = admin.Exec(ctx, `CREATE EXTENSION IF NOT EXISTS pgcrypto; DROP SCHEMA IF EXISTS it_outbox CASCADE; CREATE SCHEMA it_outbox`)
	require.NoError(t, err)
	admin.Close()

	cfg, err := pgxpool.ParseConfig(dsn)
	require.NoError(t, err)
	cfg.ConnConfig.RuntimeParams["search_path"] = "it_outbox"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS it_outbox CASCADE`)
		pool.Close()
	})
	_, err = pool.Exec(ctx, `
		CREATE TABLE outbox_events (
			id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
			routing_key text NOT NULL,
			aggregate_id uuid NOT NULL,
			schema_version integer NOT NULL DEFAULT 1,
			payload jsonb NOT NULL,
			occurred_at timestamptz NOT NULL DEFAULT now(),
			available_at timestamptz NOT NULL DEFAULT now(),
			published_at timestamptz,
			publish_attempts integer NOT NULL DEFAULT 0,
			last_error text
		)`)
	require.NoError(t, err)
	return pool
}

func write(t *testing.T, pool *pgxpool.Pool, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		require.NoError(t, outbox.Write(context.Background(), pool, outbox.Message{
			RoutingKey: "payment.succeeded", AggregateID: aggregateID, Payload: map[string]int{"i": i},
		}))
	}
}

func pending(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT count(*) FROM outbox_events WHERE published_at IS NULL`).Scan(&n))
	return n
}

// fakePub records published envelopes and can be switched to fail (broker down).
type fakePub struct {
	mu    sync.Mutex
	down  bool
	delay time.Duration
	seen  map[string]int // messageId → jumlah publish
}

func newFakePub() *fakePub { return &fakePub{seen: map[string]int{}} }

func (f *fakePub) Publish(_ context.Context, _ string, env rabbitmq.Envelope) error {
	time.Sleep(f.delay)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		return errors.New("rabbitmq: confirm timeout")
	}
	f.seen[env.MessageID]++
	return nil
}

func (f *fakePub) setDown(v bool) { f.mu.Lock(); f.down = v; f.mu.Unlock() }

func TestWrite_IsAtomicWithCallerTransaction(t *testing.T) {
	ctx := context.Background()
	pool := setup(t)

	err := postgres.WithTx(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		require.NoError(t, outbox.Write(ctx, tx, outbox.Message{RoutingKey: "payment.failed", AggregateID: aggregateID, Payload: map[string]string{"reason": "deny"}}))
		return errors.New("state change gagal")
	})
	require.Error(t, err)
	assert.Equal(t, 0, pending(t, pool), "rollback transaksi pemanggil membatalkan event")

	require.NoError(t, postgres.WithTx(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		return outbox.Write(ctx, tx, outbox.Message{RoutingKey: "payment.failed", AggregateID: aggregateID, Payload: map[string]string{"reason": "deny"}})
	}))
	assert.Equal(t, 1, pending(t, pool))
}

func TestRelay_PublishesWithRowIdAsMessageId(t *testing.T) {
	ctx := context.Background()
	pool := setup(t)
	write(t, pool, 3)
	pub := newFakePub()

	n, err := outbox.NewRelay(pool, pub, "payment-service", quiet).RunOnce(ctx)
	require.NoError(t, err)
	assert.Equal(t, 3, n)
	assert.Equal(t, 0, pending(t, pool))

	rows, err := pool.Query(ctx, `SELECT id::text FROM outbox_events`)
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var id string
		require.NoError(t, rows.Scan(&id))
		assert.Equal(t, 1, pub.seen[id], "messageId = id baris outbox")
	}
}

// Property 2: No lost outbox event — broker down menahan event; setelah pulih terkirim.
func TestRelay_BrokerDownKeepsRowPendingThenRecovers(t *testing.T) {
	ctx := context.Background()
	pool := setup(t)
	write(t, pool, 5)
	pub := newFakePub()
	pub.setDown(true)
	relay := outbox.NewRelay(pool, pub, "payment-service", quiet)

	n, err := relay.RunOnce(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, n)
	assert.Equal(t, 5, pending(t, pool), "tidak ada event hilang")

	var attempts, backedOff int
	var lastErr string
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE publish_attempts = 1), count(*) FILTER (WHERE available_at > now()) FROM outbox_events`).Scan(&attempts, &backedOff))
	assert.Equal(t, 3, attempts, "batch berhenti setelah 3 kegagalan beruntun")
	assert.Equal(t, 3, backedOff, "baris gagal ditunda dengan backoff")
	require.NoError(t, pool.QueryRow(ctx, `SELECT last_error FROM outbox_events WHERE publish_attempts = 1 LIMIT 1`).Scan(&lastErr))
	assert.Contains(t, lastErr, "confirm timeout")

	// Segera setelahnya: baris yang di-backoff tidak dicoba lagi, sisanya (2) dicoba dan gagal.
	pub.setDown(false)
	n, err = relay.RunOnce(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, n, "hanya baris yang belum di-backoff")

	_, err = pool.Exec(ctx, `UPDATE outbox_events SET available_at = now() WHERE published_at IS NULL`)
	require.NoError(t, err)
	n, err = relay.RunOnce(ctx)
	require.NoError(t, err)
	assert.Equal(t, 3, n)
	assert.Equal(t, 0, pending(t, pool), "setelah broker pulih semua terkirim")
}

// Property 3: Outbox single publisher per cycle — dua relay bersamaan tidak mempublikasikan baris yang sama.
func TestRelay_TwoRelaysNeverPublishSameRowInOneCycle(t *testing.T) {
	ctx := context.Background()
	pool := setup(t)
	const rows = 60
	write(t, pool, rows)
	pub := newFakePub()
	pub.delay = 5 * time.Millisecond

	var total atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			relay := outbox.NewRelay(pool, pub, "payment-service", quiet)
			n, err := relay.RunOnce(ctx)
			assert.NoError(t, err)
			total.Add(int32(n)) //nolint:gosec // jumlah kecil
		}()
	}
	wg.Wait()

	assert.EqualValues(t, rows, total.Load(), "semua baris terkirim tepat satu kali")
	assert.Len(t, pub.seen, rows)
	for id, n := range pub.seen {
		assert.Equal(t, 1, n, "baris %s dipublish %d kali", id, n)
	}
}

// Publish nyata ke RabbitMQ dengan publisher confirm; consumer menerima envelope dengan messageId = id baris.
func TestRelay_PublishesToRealRabbitMQ(t *testing.T) {
	url := os.Getenv("TEST_RABBITMQ_URL")
	if url == "" {
		t.Skip("TEST_RABBITMQ_URL tidak diset")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool := setup(t)
	write(t, pool, 1)

	conn, err := rabbitmq.Dial(rabbitmq.Config{URL: url, Exchange: "rusuh.it", ExchangeType: "topic", Workers: 1, Prefetch: 1,
		MaxRetries: 1, RetryDelays: []time.Duration{time.Second}, ConfirmWait: 5 * time.Second})
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	pub, err := conn.NewPublisher()
	require.NoError(t, err)
	defer func() { _ = pub.Close() }()

	got := make(chan rabbitmq.Delivery, 1)
	queue := "it.outbox-" + rabbitmq.NewID()[:8]
	go func() {
		_ = conn.Consume(ctx, rabbitmq.Subscription{Queue: queue, RoutingKeys: []string{"payment.succeeded"},
			Handler: func(_ context.Context, d rabbitmq.Delivery) error { got <- d; return nil }}, quiet)
	}()
	time.Sleep(500 * time.Millisecond) // topologi terpasang sebelum publish

	n, err := outbox.NewRelay(pool, pub, "payment-service", quiet).RunOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, n)

	var id string
	require.NoError(t, pool.QueryRow(ctx, `SELECT id::text FROM outbox_events`).Scan(&id))
	select {
	case d := <-got:
		assert.Equal(t, id, d.Envelope.MessageID)
		assert.Equal(t, "payment-service", d.Envelope.Producer)
		assert.Equal(t, "payment.succeeded", d.Envelope.EventType)
		assert.JSONEq(t, `{"i":0}`, string(d.Envelope.Payload))
	case <-time.After(10 * time.Second):
		t.Fatal("envelope tidak diterima consumer")
	}
}
