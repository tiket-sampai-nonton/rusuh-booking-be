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
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/deployments/migrations"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

const testDB = "rbt_it_queue"

// setup creates a throw-away database whose "queue" schema holds the real migrations, like the service's role sees it.
func setup(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN tidak diset")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	_, err = admin.Exec(ctx, `DROP DATABASE IF EXISTS `+testDB+` WITH (FORCE)`)
	require.NoError(t, err)
	_, err = admin.Exec(ctx, `CREATE DATABASE `+testDB)
	require.NoError(t, err)

	cfg, err := pgxpool.ParseConfig(dsn)
	require.NoError(t, err)
	cfg.ConnConfig.Database = testDB
	setupCfg := cfg.Copy()
	root, err := pgxpool.NewWithConfig(ctx, setupCfg)
	require.NoError(t, err)
	_, err = root.Exec(ctx, `CREATE SCHEMA queue`)
	require.NoError(t, err)
	migs, err := migrate.Load(migrations.FS, ".")
	require.NoError(t, err)
	runner, err := migrate.NewRunner(root, "queue", migs, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)
	_, err = runner.Up(ctx, 0)
	require.NoError(t, err)
	root.Close()

	cfg.ConnConfig.RuntimeParams["search_path"] = "queue"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(context.Background(), `DROP DATABASE IF EXISTS `+testDB+` WITH (FORCE)`)
		admin.Close()
	})
	return pool
}

func TestEventSettings_GetEnsureAndList(t *testing.T) {
	pool := setup(t)
	ctx := context.Background()
	repo := NewEventSettings(pool)
	ev := "11111111-1111-1111-1111-111111111111"

	_, found, err := repo.Get(ctx, ev)
	require.NoError(t, err)
	assert.False(t, found, "belum ada pengaturan")
	_, found, err = repo.Get(ctx, "bukan-uuid")
	require.NoError(t, err)
	assert.False(t, found, "id tidak valid = tidak ada, bukan error")
	ids, err := repo.ListEventIDs(ctx)
	require.NoError(t, err)
	assert.Empty(t, ids)

	s, err := repo.Ensure(ctx, ev)
	require.NoError(t, err)
	assert.Equal(t, ev, s.EventID)
	assert.Equal(t, 500, s.ActiveSlotCapacity, "bawaan kolom")
	assert.True(t, s.OneActivePerVisitor)
	assert.True(t, s.OneActivePerEmail)

	_, err = pool.Exec(ctx, `UPDATE event_settings SET active_slot_capacity = 20 WHERE event_id = $1::uuid`, ev)
	require.NoError(t, err)
	again, err := repo.Ensure(ctx, ev)
	require.NoError(t, err)
	assert.Equal(t, 20, again.ActiveSlotCapacity, "Ensure tidak menimpa pengaturan yang sudah ada")

	got, found, err := repo.Get(ctx, ev)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, 20, got.ActiveSlotCapacity)
	ids, err = repo.ListEventIDs(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{ev}, ids)
}

func TestEntryAudit_AppendsForGuestAndAccount(t *testing.T) {
	pool := setup(t)
	ctx := context.Background()
	repo := NewEntryAudit(pool)
	rec := outbound.AuditRecord{
		EventID: "11111111-1111-1111-1111-111111111111", EntryID: "22222222-2222-2222-2222-222222222222", Seq: 7,
		BuyerNikHash: []byte{0xaa}, Action: "JOINED", OccurredAt: time.Now(),
	}
	require.NoError(t, repo.Append(ctx, rec), "tamu: account_id NULL, metadata kosong")
	rec.Action, rec.AccountID, rec.Metadata = "ADMITTED", "33333333-3333-3333-3333-333333333333", map[string]any{"via": "direct"}
	require.NoError(t, repo.Append(ctx, rec))

	var n, withAccount int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*), count(account_id) FROM entry_audit`).Scan(&n, &withAccount))
	assert.Equal(t, 2, n)
	assert.Equal(t, 1, withAccount)

	_, err := pool.Exec(ctx, `UPDATE entry_audit SET action='X'`)
	assert.Error(t, err, "tabel audit append-only")
	rec.EventID = "bukan-uuid"
	assert.Error(t, repo.Append(ctx, rec))
}
