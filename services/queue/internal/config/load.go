package config

import (
	"errors"
	"fmt"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/dotenv"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/logger"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/postgres"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/rabbitmq"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/valkey"
)

// Load reads the environment and validates every value. Semua kesalahan dilaporkan sekaligus.
func Load() (Config, error) {
	var r dotenv.Reader
	var parseErrs []error
	cfg := Config{
		App: App{
			Env:  r.String("APP_ENV", "local"),
			Name: r.String("APP_NAME", ServiceName),
			Port: r.Int("APP_PORT", 8081),
		},
		Log: logger.Config{Level: r.String("LOG_LEVEL", "info"), Format: r.String("LOG_FORMAT", "json")},
		HTTP: HTTP{
			ShutdownTimeout: r.Duration("HTTP_SHUTDOWN_TIMEOUT", 15*time.Second),
			HealthTimeout:   r.Duration("HEALTH_TIMEOUT", 2*time.Second),
		},
		Worker: Worker{Interval: r.Duration("WORKER_INTERVAL", 5*time.Second)},
	}

	cfg.Postgres = postgres.Config{
		Host:     r.String("POSTGRES_HOST", "localhost"),
		Port:     r.Int("POSTGRES_PORT", 5432),
		User:     r.String("POSTGRES_USER", "svc_queue"),
		Password: r.String("POSTGRES_PASSWORD", ""),
		Database: r.String("POSTGRES_DATABASE", "rbt"),
		SSLMode:  r.String("POSTGRES_SSLMODE", "disable"),
		Schema:   r.String("POSTGRES_SCHEMA", "queue"),
		MaxConns: r.Int("POSTGRES_MAX_CONNS", 10),
	}

	cfg.Valkey = valkey.Config{
		Server:    r.String("VALKEY_SERVER", `[{"host":"localhost","port":6379}]`),
		Username:  r.String("VALKEY_USERNAME", ""),
		Password:  r.String("VALKEY_PASSWORD", ""),
		TLS:       r.Bool("VALKEY_TLS", false),
		DB:        r.Int("VALKEY_DB", 0),
		PrefixEnv: r.String("VALKEY_PREFIX_ENV", ""),
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

	cfg.Token = Token{
		Issuer:         r.String("TOKEN_ISSUER", "rbt-queue"),
		Audience:       r.String("TOKEN_AUDIENCE", "rbt"),
		KeyID:          r.String("TOKEN_KEY_ID", "queue-1"),
		PrivateKeyFile: r.String("TOKEN_PRIVATE_KEY_FILE", ""),
		PublicKeyFile:  r.String("TOKEN_PUBLIC_KEY_FILE", ""),
		AdmissionTTL:   r.Duration("TOKEN_ADMISSION_TTL", 60*time.Second),
		VisitorTTL:     r.Duration("TOKEN_VISITOR_TTL", 24*time.Hour),
		QueueTTL:       r.Duration("TOKEN_QUEUE_TTL", time.Hour),
	}
	cfg.PII = PII{
		EncKeyHex:  r.String("PII_ENC_KEY", ""),
		HashKeyHex: r.String("PII_HMAC_KEY", ""),
	}
	cfg.Booking = Booking{
		BaseURL: r.String("BOOKING_SERVICE_URL", ""),
		Timeout: r.Duration("BOOKING_TIMEOUT", 3*time.Second),
	}
	cfg.RateLimit = RateLimit{
		JoinPerEmailWindow: r.Duration("RATE_LIMIT_JOIN_PER_EMAIL_WINDOW", time.Minute),
		JoinPerIPWindow:    r.Duration("RATE_LIMIT_JOIN_PER_IP_WINDOW", time.Minute),
		MePerVisitorWindow: r.Duration("RATE_LIMIT_ME_PER_VISITOR_WINDOW", time.Minute),
		JoinPerEmail:       r.Int("RATE_LIMIT_JOIN_PER_EMAIL", 5),
		JoinPerIP:          r.Int("RATE_LIMIT_JOIN_PER_IP", 60),
		MePerVisitor:       r.Int("RATE_LIMIT_ME_PER_VISITOR", 120),
	}
	cfg.WorkerTuning = WorkerTuning{
		PromotorInterval: r.Duration("QUEUE_PROMOTOR_INTERVAL_MS", 100*time.Millisecond),
		SweeperInterval:  r.Duration("QUEUE_SWEEPER_INTERVAL_MS", 10*time.Second),
		StatusInterval:   r.Duration("QUEUE_STATUS_INTERVAL_MS", time.Second),
		PromoteBatchMax:  r.Int("QUEUE_PROMOTE_BATCH_MAX", 200),
	}

	// Kesalahan parsing dan validasi dilaporkan bersama agar semua variable yang salah terlihat sekaligus.
	if err := errors.Join(append(parseErrs, r.Err(), cfg.Validate())...); err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	return cfg, nil
}
