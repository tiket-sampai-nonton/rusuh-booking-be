//go:build integration

package postgres

import (
	"context"
	"io"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/migrate"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/deployments/migrations"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/domain/mapper"
)

const (
	msgID     = "11111111-1111-1111-1111-111111111111"
	bookingID = "22222222-2222-2222-2222-222222222222"
)

// setup membuat schema it_notification baru dan menjalankan migrasi yang sama dengan `notification migrate up`.
func setup(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN tidak diset")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	_, err = admin.Exec(ctx, `CREATE EXTENSION IF NOT EXISTS pgcrypto; DROP SCHEMA IF EXISTS it_notification CASCADE; CREATE SCHEMA it_notification`)
	require.NoError(t, err)
	admin.Close()

	cfg, err := pgxpool.ParseConfig(dsn)
	require.NoError(t, err)
	cfg.ConnConfig.RuntimeParams["search_path"] = "it_notification"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS it_notification CASCADE`)
		pool.Close()
	})
	migs, err := migrate.Load(migrations.FS, ".")
	require.NoError(t, err)
	r, err := migrate.NewRunner(pool, "it_notification", migs, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)
	_, err = r.Up(ctx, 0)
	require.NoError(t, err)
	return pool
}

func claim() entity.Delivery {
	return mapper.NewEmailDelivery(msgID, entity.TypeBookingPaid, bookingID, []byte("recipient-hash"))
}

func status(t *testing.T, pool *pgxpool.Pool) (string, int) {
	t.Helper()
	var s string
	var attempts int
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT status, attempts FROM deliveries WHERE source_message_id = $1::uuid AND notification_type = $2`,
		msgID, string(entity.TypeBookingPaid)).Scan(&s, &attempts))
	return s, attempts
}

// Property 2: Retry eventually sends — pesan dari retry queue harus bisa mengklaim ulang baris RETRY.
func TestClaimDelivery_RetryIsClaimedAgain(t *testing.T) {
	ctx := context.Background()
	pool := setup(t)
	repo := NewDeliveries(pool)

	ok, err := repo.ClaimDelivery(ctx, claim())
	require.NoError(t, err)
	assert.True(t, ok, "baris baru diklaim")

	again, err := repo.ClaimDelivery(ctx, claim())
	require.NoError(t, err)
	assert.False(t, again, "SENDING yang lease-nya belum habis dipegang worker lain")

	require.NoError(t, repo.BumpAttempt(ctx, msgID, entity.TypeBookingPaid, "smtp down", time.Now().Add(5*time.Second)))
	st, attempts := status(t, pool)
	assert.Equal(t, "RETRY", st)
	assert.Equal(t, 1, attempts)

	ok, err = repo.ClaimDelivery(ctx, claim())
	require.NoError(t, err)
	assert.True(t, ok, "RETRY diklaim ulang sehingga email dikirim ulang")
	st, attempts = status(t, pool)
	assert.Equal(t, "SENDING", st)
	assert.Equal(t, 1, attempts, "attempts hanya naik lewat BumpAttempt")
}

// Property 1: At most one send after success — SENT dan DEAD tidak pernah diklaim lagi.
func TestClaimDelivery_FinalStatesAreSkipped(t *testing.T) {
	ctx := context.Background()
	pool := setup(t)
	repo := NewDeliveries(pool)

	_, err := repo.ClaimDelivery(ctx, claim())
	require.NoError(t, err)
	require.NoError(t, repo.MarkSent(ctx, msgID, entity.TypeBookingPaid, "provider-1"))
	ok, err := repo.ClaimDelivery(ctx, claim())
	require.NoError(t, err)
	assert.False(t, ok, "SENT → duplikat, tidak dikirim lagi")

	_, err = pool.Exec(ctx, `UPDATE deliveries SET status = 'DEAD'`)
	require.NoError(t, err)
	ok, err = repo.ClaimDelivery(ctx, claim())
	require.NoError(t, err)
	assert.False(t, ok, "DEAD → tidak dicoba lagi")
}

// Crash setelah klaim tetapi sebelum kirim: setelah lease habis, redelivery broker boleh mengklaim ulang.
func TestClaimDelivery_StaleSendingIsReclaimed(t *testing.T) {
	ctx := context.Background()
	pool := setup(t)
	repo := NewDeliveries(pool)

	_, err := repo.ClaimDelivery(ctx, claim())
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE deliveries SET next_attempt_at = now() - interval '1 second'`)
	require.NoError(t, err)

	ok, err := repo.ClaimDelivery(ctx, claim())
	require.NoError(t, err)
	assert.True(t, ok, "SENDING basi diklaim ulang")
}

// Klaim bersamaan untuk pesan yang sama: tepat satu pemenang (tidak ada kirim ganda).
func TestClaimDelivery_ConcurrentClaimsHaveOneWinner(t *testing.T) {
	ctx := context.Background()
	pool := setup(t)
	repo := NewDeliveries(pool)

	const workers = 20
	var winners atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := repo.ClaimDelivery(ctx, claim())
			if assert.NoError(t, err) && ok {
				winners.Add(1)
			}
		}()
	}
	wg.Wait()
	assert.EqualValues(t, 1, winners.Load())
}

// DEAD dan expired adalah status akhir: tidak pernah diklaim ulang, alasan tersimpan.
func TestMarkDeadAndMarkExpired_AreFinal(t *testing.T) {
	ctx := context.Background()
	pool := setup(t)
	repo := NewDeliveries(pool)

	_, err := repo.ClaimDelivery(ctx, claim())
	require.NoError(t, err)
	require.NoError(t, repo.MarkDead(ctx, msgID, entity.TypeBookingPaid, "alamat ditolak"))
	var lastErr string
	require.NoError(t, pool.QueryRow(ctx, `SELECT last_error FROM deliveries`).Scan(&lastErr))
	assert.Equal(t, "alamat ditolak", lastErr)
	ok, err := repo.ClaimDelivery(ctx, claim())
	require.NoError(t, err)
	assert.False(t, ok)

	push := mapper.NewPushDelivery(msgID, entity.TypeTurnArrived, bookingID, []byte("h"))
	_, err = repo.ClaimDelivery(ctx, push)
	require.NoError(t, err)
	require.NoError(t, repo.MarkExpired(ctx, msgID, entity.TypeTurnArrived))
	var st string
	var expiresAt *time.Time
	require.NoError(t, pool.QueryRow(ctx, `SELECT status, expires_at FROM deliveries WHERE notification_type = 'TURN_ARRIVED'`).Scan(&st, &expiresAt))
	assert.Equal(t, "DEAD", st)
	assert.NotNil(t, expiresAt, "push yang terlambat dicatat expired, tidak di-retry")
	ok, err = repo.ClaimDelivery(ctx, push)
	require.NoError(t, err)
	assert.False(t, ok)
}
