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

// TC-ANON-15: migration 0009 backfills live rows, is reversible while data allows, and refuses a
// lossy rollback.
func TestMigration0009_UpDownBackfill(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN tidak diset")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	defer admin.Close()
	const db = "aqb_mig0009"
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

	n, err := r.Up(ctx, 8)
	require.NoError(t, err)
	require.Equal(t, 8, n)

	// Live rows created under 0008: RESERVED (3 units), EDITABLE (3 units, booking of 1).
	_, err = pool.Exec(ctx, `
		INSERT INTO booking.events (id, slug, name, venue, starts_at, sales_starts_at, sales_ends_at, status,
			max_tickets_per_order, max_tickets_per_buyer_nik, selection_ttl_seconds, payment_ttl_seconds,
			claim_window_seconds, max_attendee_checks_per_session, published_at)
		VALUES ('e0000000-0000-0000-0000-000000000009','s','s','v',now()+interval '1 hour',now()-interval '1 hour',now()+interval '2 hours',
			'AVAILABLE',3,3,300,600,180,10,now())`)
	require.NoError(t, err)
	ins := func(id, status string, h string) {
		_, err := pool.Exec(ctx, `
			INSERT INTO booking.admissions (id, event_id, queue_entry_id, queue_seq, visitor_id, buyer_nik_hash, units, status,
				cfg_selection_ttl_seconds, cfg_payment_ttl_seconds, cfg_submit_grace_seconds, cfg_edit_enabled, cfg_max_edits,
				cfg_allow_mixed_categories, reserved_at, expires_at)
			VALUES ($1::uuid,'e0000000-0000-0000-0000-000000000009', gen_random_uuid(), 1, gen_random_uuid(), $2, 3, $3,
				300,600,5,true,3,true, now(), now()+interval '5 minutes')`, id, []byte(h), status)
		require.NoError(t, err)
	}
	ins("a0000000-0000-0000-0000-000000000001", "RESERVED", "h1")
	ins("a0000000-0000-0000-0000-000000000002", "ACTIVE", "h2")

	n, err = r.Up(ctx, 1) // only 0009: later migrations (0010+) must not change the count
	require.NoError(t, err)
	require.Equal(t, 1, n, "0009 applied")
	var o1, o2 int
	require.NoError(t, pool.QueryRow(ctx, `SELECT outstanding_units FROM booking.admissions WHERE id='a0000000-0000-0000-0000-000000000001'`).Scan(&o1))
	require.NoError(t, pool.QueryRow(ctx, `SELECT outstanding_units FROM booking.admissions WHERE id='a0000000-0000-0000-0000-000000000002'`).Scan(&o2))
	assert.Equal(t, 3, o1, "RESERVED backfilled to units")
	assert.Equal(t, 3, o2, "ACTIVE backfilled to units")

	// Two active admissions may now bind the same buyer NIK (uq_admission_active_buyer dropped).
	_, err = pool.Exec(ctx, `UPDATE booking.admissions SET buyer_nik_hash='same'`)
	assert.NoError(t, err)
	// ...and a NULL buyer hash is allowed (anonymous reservation).
	_, err = pool.Exec(ctx, `UPDATE booking.admissions SET buyer_nik_hash=NULL WHERE id='a0000000-0000-0000-0000-000000000001'`)
	require.NoError(t, err)

	// Rollback is refused while NULL buyer hashes exist, and also while two active admissions share a NIK.
	_, err = r.Down(ctx, 1)
	require.Error(t, err)
	_, err = pool.Exec(ctx, `UPDATE booking.admissions SET buyer_nik_hash='x' WHERE id='a0000000-0000-0000-0000-000000000001'`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE booking.admissions SET buyer_nik_hash='x' WHERE id='a0000000-0000-0000-0000-000000000002'`)
	require.NoError(t, err)
	_, err = r.Down(ctx, 1)
	require.Error(t, err, "duplicate active buyer hash blocks rollback")

	// Make the data compatible: rollback works, up again is idempotent on the data.
	_, err = pool.Exec(ctx, `UPDATE booking.admissions SET buyer_nik_hash='y' WHERE id='a0000000-0000-0000-0000-000000000002'`)
	require.NoError(t, err)
	_, err = r.Down(ctx, 1)
	require.NoError(t, err)
	n, err = r.Up(ctx, 1) // only 0009: later migrations (0010+) must not change the count
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	require.NoError(t, pool.QueryRow(ctx, `SELECT outstanding_units FROM booking.admissions WHERE id='a0000000-0000-0000-0000-000000000002'`).Scan(&o2))
	assert.Equal(t, 3, o2)
}
