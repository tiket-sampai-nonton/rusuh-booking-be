package config

import (
	"errors"
	"fmt"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/dotenv"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/logger"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/postgres"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/rabbitmq"
)

// Load reads the environment and validates every value. Semua kesalahan dilaporkan sekaligus.
func Load() (Config, error) {
	var r dotenv.Reader
	var parseErrs []error
	cfg := Config{
		App: App{
			Env:  r.String("APP_ENV", "local"),
			Name: r.String("APP_NAME", ServiceName),
			Port: r.Int("APP_PORT", 8083),
		},
		Log: logger.Config{Level: r.String("LOG_LEVEL", "info"), Format: r.String("LOG_FORMAT", "json")},
		HTTP: HTTP{
			ShutdownTimeout:       r.Duration("HTTP_SHUTDOWN_TIMEOUT", 15*time.Second),
			HealthTimeout:         r.Duration("HEALTH_TIMEOUT", 2*time.Second),
			RateLimitPerIP:        r.Int("RATE_LIMIT_PER_IP", 300),
			RateLimitWindow:       r.Duration("RATE_LIMIT_WINDOW", time.Minute),
			RateLimitWebhookPerIP: r.Int("RATE_LIMIT_WEBHOOK_PER_IP", 600),
		},
		Worker: Worker{Interval: r.Duration("WORKER_INTERVAL", 5*time.Second)},
	}

	cfg.Postgres = postgres.Config{
		Host:     r.String("POSTGRES_HOST", "localhost"),
		Port:     r.Int("POSTGRES_PORT", 5432),
		User:     r.String("POSTGRES_USER", "svc_payment"),
		Password: r.String("POSTGRES_PASSWORD", ""),
		Database: r.String("POSTGRES_DATABASE", "rbt"),
		SSLMode:  r.String("POSTGRES_SSLMODE", "disable"),
		Schema:   r.String("POSTGRES_SCHEMA", "payment"),
		MaxConns: r.Int("POSTGRES_MAX_CONNS", 10),
	}

	delays, err := rabbitmq.ParseDelays(r.String("RABBITMQ_RETRY_DELAYS", "5s,30s,2m"))
	if err != nil {
		parseErrs = append(parseErrs, err)
	}
	cfg.RabbitMQ = rabbitmq.Config{
		URL:          r.String("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/"),
		Exchange:     r.String("RABBITMQ_EXCHANGE", "rusuh.events"),
		ExchangeType: r.String("RABBITMQ_EXCHANGE_TYPE", "topic"),
		Workers:      r.Int("RABBITMQ_WORKERS", 4),
		Prefetch:     r.Int("RABBITMQ_PREFETCH", 20),
		MaxRetries:   r.Int("RABBITMQ_MAX_RETRIES", 3),
		RetryDelays:  delays,
		ConfirmWait:  r.Duration("RABBITMQ_CONFIRM_TIMEOUT", 5*time.Second),
	}

	cfg.Payment = loadPayment(&r)

	// Kesalahan parsing dan validasi dilaporkan bersama agar semua env yang salah terlihat sekaligus.
	if err := errors.Join(append(parseErrs, r.Err(), cfg.Validate())...); err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	return cfg, nil
}
