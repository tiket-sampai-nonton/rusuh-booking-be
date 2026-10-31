//go:build e2e

package helpers

import (
	"context"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Env holds service base URLs and shared resources for e2e tests.
type Env struct {
	BookingAPI   string
	QueueAPI     string
	UserAPI      string
	MailpitAPI   string
	AMQPURL      string
	ServiceToken string // internal service bearer for payment-lock etc.
	Pool         *pgxpool.Pool
}

// NewEnv builds Env from environment variables with local-dev defaults.
func NewEnv(ctx context.Context) (*Env, error) {
	dsn := getenv("TEST_POSTGRES_DSN", "postgres://rbt:rbt-local-only@localhost:5432/rbt?sslmode=disable")
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	return &Env{
		BookingAPI:   getenv("BOOKING_API_URL", "http://localhost:8082"),
		QueueAPI:     getenv("QUEUE_API_URL", "http://localhost:8081"),
		UserAPI:      getenv("USER_API_URL", "http://localhost:8085"),
		MailpitAPI:   getenv("MAILPIT_API_URL", "http://localhost:8025"),
		AMQPURL:      getenv("TEST_RABBITMQ_URL", "amqp://guest:guest@localhost:5672/"),
		ServiceToken: getenv("E2E_SERVICE_TOKEN", ""),
		Pool:         pool,
	}, nil
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
