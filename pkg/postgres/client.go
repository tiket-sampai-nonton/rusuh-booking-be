package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Client owns the pool. Construct with New; close with Close.
type Client struct {
	pool *pgxpool.Pool
}

// New creates the pool and pings it once so a bad DSN fails at start-up.
func New(ctx context.Context, cfg Config) (*Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	pcfg, err := pgxpool.ParseConfig(cfg.DSN())
	if err != nil {
		return nil, fmt.Errorf("postgres: parse config: %w", err)
	}
	pcfg.MaxConns = int32(cfg.MaxConns) // #nosec G115 -- dibatasi 1–200 oleh Validate //nolint:gosec
	pcfg.MinConns = 0
	pcfg.MaxConnIdleTime = 5 * time.Minute
	pcfg.HealthCheckPeriod = 30 * time.Second

	pool, err := pgxpool.NewWithConfig(ctx, pcfg)
	if err != nil {
		return nil, fmt.Errorf("postgres: open pool %s: %w", cfg.Redacted(), err)
	}
	c := &Client{pool: pool}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := c.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: ping %s: %w", cfg.Redacted(), err)
	}
	return c, nil
}

// Pool exposes the pool to repositories.
func (c *Client) Pool() *pgxpool.Pool { return c.pool }

// Name implements the health Pinger contract.
func (c *Client) Name() string { return "postgres" }

// Ping checks connectivity.
func (c *Client) Ping(ctx context.Context) error { return c.pool.Ping(ctx) }

// Close releases every connection.
func (c *Client) Close() { c.pool.Close() }

// WithTx runs fn in one transaction. fn's error rolls back; a nil error commits.
func (c *Client) WithTx(ctx context.Context, opts pgx.TxOptions, fn func(pgx.Tx) error) error {
	return WithTx(ctx, c.pool, opts, fn)
}

// TxBeginner is satisfied by *pgxpool.Pool and *pgx.Conn.
type TxBeginner interface {
	BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error)
}

// WithTx runs fn inside a transaction started from db.
func WithTx(ctx context.Context, db TxBeginner, opts pgx.TxOptions, fn func(pgx.Tx) error) (err error) {
	tx, err := db.BeginTx(ctx, opts)
	if err != nil {
		return fmt.Errorf("postgres: begin: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(ctx)
			panic(p)
		}
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()
	if err = fn(tx); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: commit: %w", err)
	}
	return nil
}

// SQLSTATE codes used by repositories to map database errors to domain errors.
const (
	CodeUniqueViolation     = "23505"
	CodeCheckViolation      = "23514"
	CodeForeignKeyViolation = "23503"
	CodeSerialization       = "40001"
	CodeDeadlock            = "40P01"
	CodeRestrictViolation   = "23001"
)

// IsCode reports whether err is a PostgreSQL error with the given SQLSTATE.
func IsCode(err error, code string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code
}

// ConstraintName returns the violated constraint name, or "".
func ConstraintName(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.ConstraintName
	}
	return ""
}
