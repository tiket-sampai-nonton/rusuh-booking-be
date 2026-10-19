//go:build integration

package postgres

import (
	"context"
	"errors"
	"os"
	"strconv"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// IntegrationConfig reads TEST_POSTGRES_* (dipakai juga oleh paket lain lewat salinan kecil).
func integrationConfig(t *testing.T) Config {
	t.Helper()
	host := os.Getenv("TEST_POSTGRES_HOST")
	if host == "" {
		t.Skip("TEST_POSTGRES_HOST tidak diset")
	}
	port, _ := strconv.Atoi(os.Getenv("TEST_POSTGRES_PORT"))
	return Config{Host: host, Port: port, User: os.Getenv("TEST_POSTGRES_USER"), Password: os.Getenv("TEST_POSTGRES_PASSWORD"),
		Database: os.Getenv("TEST_POSTGRES_DATABASE"), SSLMode: "disable", Schema: "public", MaxConns: 4}
}

func TestClient_PingAndTx(t *testing.T) {
	ctx := context.Background()
	c, err := New(ctx, integrationConfig(t))
	require.NoError(t, err)
	defer c.Close()

	assert.NoError(t, c.Ping(ctx))
	assert.Equal(t, "postgres", c.Name())

	_, err = c.Pool().Exec(ctx, `DROP TABLE IF EXISTS rbt_it_tx; CREATE TABLE rbt_it_tx (id int PRIMARY KEY)`)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = c.Pool().Exec(ctx, `DROP TABLE IF EXISTS rbt_it_tx`) })

	boom := errors.New("boom")
	err = c.WithTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO rbt_it_tx VALUES (1)`); err != nil {
			return err
		}
		return boom
	})
	assert.ErrorIs(t, err, boom)

	require.NoError(t, c.WithTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO rbt_it_tx VALUES (2)`)
		return err
	}))

	var n int
	require.NoError(t, c.Pool().QueryRow(ctx, `SELECT count(*) FROM rbt_it_tx`).Scan(&n))
	assert.Equal(t, 1, n, "rollback harus membuang baris 1, commit menyimpan baris 2")

	err = c.WithTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO rbt_it_tx VALUES (2)`)
		return err
	})
	assert.True(t, IsCode(err, CodeUniqueViolation))
}
