//go:build integration

package postgres

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/migrate"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/deployments/migrations"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
)

// insertDraftEvent inserts a DRAFT event whose sales window opens at salesStartsAt.
func insertDraftEvent(t *testing.T, f fixture, slug string, salesStartsAt time.Time) string {
	t.Helper()
	var id string
	require.NoError(t, f.pool.QueryRow(context.Background(), `
		INSERT INTO booking.events (slug, name, venue, starts_at, sales_starts_at, sales_ends_at, status)
		VALUES ($1, $1, 'GBK', now() + interval '3 days', $2, now() + interval '2 days', 'DRAFT')
		RETURNING id::text`, slug, salesStartsAt).Scan(&id))
	return id
}

// TC-CFG-02 (D-C4.1): rules lock when the sales window opens, whatever the status; the lock is what the
// guard trigger enforces, so the admin PATCH (which cannot send rule fields) is covered by the trigger.
func TestRulesLock_StartsAtSalesStartRegardlessOfStatus(t *testing.T) {
	f := seed(t)
	ctx := context.Background()
	started := insertDraftEvent(t, f, "rl-started", time.Now().Add(-time.Minute))
	upcoming := insertDraftEvent(t, f, "rl-upcoming", time.Now().Add(time.Hour))

	_, err := f.pool.Exec(ctx, `UPDATE booking.events SET max_tickets_per_order = 2 WHERE id = $1::uuid`, started)
	require.Error(t, err, "rules of a started DRAFT event are locked")
	assert.ErrorIs(t, mapEventWriteErr(err, "x"), inbound.ErrRulesLocked)

	_, err = f.pool.Exec(ctx, `UPDATE booking.events SET max_tickets_per_order = 2 WHERE id = $1::uuid`, upcoming)
	require.NoError(t, err, "rules of a DRAFT event before its sales window stay editable")
	assert.Equal(t, 0, f.n(t, `SELECT count(*) FROM booking.events WHERE id=$1::uuid AND rules_locked_at IS NOT NULL`, upcoming))

	ttl := 240 // timers are not guarded: still editable after the sales window opened
	require.NoError(t, NewEventWriteRepo(f.pool).UpdateConfig(ctx, started, dto.UpdateEventConfigRequest{SelectionTTLSeconds: &ttl}))
}

// TC-CFG-02 (D-C4.1): migration 0013 backfills rules_locked_at for events whose sales window already opened, and
// records the backfill in config_audit under the migration actor.
func TestMigration0013_BackfillsStartedEvents(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN tidak diset")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	defer admin.Close()
	const db = "aqb_mig0013"
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

	n, err := r.Up(ctx, 12)
	require.NoError(t, err)
	require.Equal(t, 12, n)

	var started, upcoming string
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO booking.events (slug, name, venue, starts_at, sales_starts_at, sales_ends_at, status)
		VALUES ('mig13-started', 'mig13', 'GBK', now() + interval '3 days', now() - interval '1 minute', now() + interval '2 days', 'DRAFT')
		RETURNING id::text`).Scan(&started))
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO booking.events (slug, name, venue, starts_at, sales_starts_at, sales_ends_at, status)
		VALUES ('mig13-upcoming', 'mig13', 'GBK', now() + interval '3 days', now() + interval '1 day', now() + interval '2 days', 'DRAFT')
		RETURNING id::text`).Scan(&upcoming))

	n, err = r.Up(ctx, 1) // only 0013
	require.NoError(t, err)
	require.Equal(t, 1, n, "0013 applied")

	var lockedMatchesSalesStart bool
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT rules_locked_at = sales_starts_at FROM booking.events WHERE id=$1::uuid`, started).Scan(&lockedMatchesSalesStart))
	assert.True(t, lockedMatchesSalesStart, "started event: rules_locked_at = sales_starts_at")
	var upcomingLocked bool
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT rules_locked_at IS NOT NULL FROM booking.events WHERE id=$1::uuid`, upcoming).Scan(&upcomingLocked))
	assert.False(t, upcomingLocked, "event whose sales window has not opened stays unlocked")

	var audits int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM booking.config_audit WHERE event_id=$1::uuid AND changed_by='migration-0013'`, started).Scan(&audits))
	assert.Equal(t, 1, audits, "one config_audit row for the backfill")
}
