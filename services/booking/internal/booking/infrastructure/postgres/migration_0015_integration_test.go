//go:build integration

package postgres

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/migrate"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/deployments/migrations"
)

// AC-C7.8 (D-C7.8): migration 0015 adds the two retention indexes and its down removes them, leaving the rows alone.
func TestMigration0015_RetentionIndexes(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN tidak diset")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	defer admin.Close()
	const db = "aqb_mig0015"
	_, err = admin.Exec(ctx, `DROP DATABASE IF EXISTS `+db+` WITH (FORCE)`)
	require.NoError(t, err)
	_, err = admin.Exec(ctx, `CREATE DATABASE `+db)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = admin.Exec(context.Background(), `DROP DATABASE IF EXISTS `+db+` WITH (FORCE)`) })

	cfg, err := pgxpool.ParseConfig(dsn)
	require.NoError(t, err)
	cfg.ConnConfig.Database = db
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	defer pool.Close()
	_, err = pool.Exec(ctx, `CREATE SCHEMA booking`)
	require.NoError(t, err)
	migs, err := migrate.Load(migrations.FS, ".")
	require.NoError(t, err)
	r, err := migrate.NewRunner(pool, "booking", migs, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)

	n, err := r.Up(ctx, 14)
	require.NoError(t, err)
	require.Equal(t, 14, n)
	assert.Equal(t, 0, retentionIndexCount(t, pool), "before 0015 neither index exists")

	n, err = r.Up(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, 1, n, "0015 applied")
	assert.Equal(t, 2, retentionIndexCount(t, pool), "0015 up: both indexes exist")

	n, err = r.Down(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, 1, n, "0015 reverted")
	assert.Equal(t, 0, retentionIndexCount(t, pool), "0015 down: both indexes removed")
}

func retentionIndexCount(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(context.Background(), `
		SELECT count(*) FROM pg_indexes
		 WHERE schemaname = 'booking' AND indexname IN ('idx_booking_outbox_published', 'idx_admissions_ended')`).Scan(&n))
	return n
}
