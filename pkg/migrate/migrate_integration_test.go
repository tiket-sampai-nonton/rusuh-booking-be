//go:build integration

package migrate

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN tidak diset")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool
}

func TestRunner_UpDownStatusAndConcurrency(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := "it_migrate"
	_, err := pool.Exec(ctx, fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE; CREATE SCHEMA %s", schema, schema))
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", schema)) })

	fsys := fstest.MapFS{
		"m/0001_init.up.sql":   {Data: []byte("CREATE TABLE a (id int PRIMARY KEY);")},
		"m/0001_init.down.sql": {Data: []byte("DROP TABLE a;")},
		"m/0002_more.up.sql":   {Data: []byte("CREATE TABLE b (id int PRIMARY KEY);")},
		"m/0002_more.down.sql": {Data: []byte("DROP TABLE b;")},
	}
	migs, err := Load(fsys, "m")
	require.NoError(t, err)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Lima instance start bersamaan: advisory lock memastikan setiap migrasi diterapkan tepat sekali.
	var wg sync.WaitGroup
	total := make([]int, 5)
	errs := make([]error, 5)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r, err := NewRunner(pool, schema, migs, logger)
			require.NoError(t, err)
			total[i], errs[i] = r.Up(ctx, 0)
		}(i)
	}
	wg.Wait()
	sum := 0
	for i := range total {
		require.NoError(t, errs[i])
		sum += total[i]
	}
	assert.Equal(t, 2, sum)

	r, err := NewRunner(pool, schema, migs, logger)
	require.NoError(t, err)
	st, err := r.Status(ctx)
	require.NoError(t, err)
	assert.True(t, st[0].Applied && st[1].Applied)

	n, err := r.Down(ctx, 0)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	var exists bool
	require.NoError(t, pool.QueryRow(ctx, `SELECT to_regclass('it_migrate.b') IS NOT NULL`).Scan(&exists))
	assert.False(t, exists)

	// Drift: isi migrasi 0001 diubah setelah diterapkan.
	fsys["m/0001_init.up.sql"] = &fstest.MapFile{Data: []byte("CREATE TABLE a (id bigint PRIMARY KEY);")}
	changed, err := Load(fsys, "m")
	require.NoError(t, err)
	r2, err := NewRunner(pool, schema, changed, logger)
	require.NoError(t, err)
	_, err = r2.Up(ctx, 0)
	assert.ErrorContains(t, err, "berubah setelah diterapkan")
}

func TestRunner_FailedMigrationLeavesNothing(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	schema := "it_migrate_fail"
	_, err := pool.Exec(ctx, fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE; CREATE SCHEMA %s", schema, schema))
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", schema)) })

	migs, err := Load(fstest.MapFS{
		"m/0001_bad.up.sql":   {Data: []byte("CREATE TABLE a (id int); SELECT 1/0;")},
		"m/0001_bad.down.sql": {Data: []byte("DROP TABLE a;")},
	}, "m")
	require.NoError(t, err)
	r, err := NewRunner(pool, schema, migs, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)
	_, err = r.Up(ctx, 0)
	assert.Error(t, err)
	var exists bool
	require.NoError(t, pool.QueryRow(ctx, `SELECT to_regclass('it_migrate_fail.a') IS NOT NULL`).Scan(&exists))
	assert.False(t, exists)
}
