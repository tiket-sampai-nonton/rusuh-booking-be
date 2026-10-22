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
			Port: r.Int("APP_PORT", 8084),
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
		User:     r.String("POSTGRES_USER", "svc_notification"),
		Password: r.String("POSTGRES_PASSWORD", ""),
		Database: r.String("POSTGRES_DATABASE", "rbt"),
		SSLMode:  r.String("POSTGRES_SSLMODE", "disable"),
		Schema:   r.String("POSTGRES_SCHEMA", "notification"),
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

	cfg.SMTP = SMTP{
		Host:     r.String("SMTP_HOST", "localhost"),
		Port:     r.Int("SMTP_PORT", 1025),
		User:     r.String("SMTP_USER", ""),
		Password: r.String("SMTP_PASSWORD", ""),
		Timeout:  r.Duration("SMTP_TIMEOUT", 10*time.Second),
	}
	// EMAIL_FROM menggantikan SMTP_FROM; SMTP_FROM tetap dibaca sebagai cadangan. Default pengirim
	// hanya untuk mailpit: resend wajib memakai EMAIL_FROM eksplisit (domain terverifikasi).
	provider := r.String("EMAIL_PROVIDER", EmailMailpit)
	from := r.String("EMAIL_FROM", r.String("SMTP_FROM", ""))
	if from == "" && provider == EmailMailpit {
		from = "no-reply@rusuh.example"
	}
	cfg.Email = Email{Provider: provider, From: from, ResendAPIKey: r.String("RESEND_API_KEY", "")}
	cfg.FCM = FCM{
		ProjectID:       r.String("FCM_PROJECT_ID", ""),
		CredentialsPath: r.String("FCM_CREDENTIALS_PATH", ""),
	}
	cfg.PII = PII{
		EncKeyHex:  r.String("PII_ENC_KEY", ""),
		HashKeyHex: r.String("PII_HASH_KEY", ""),
	}
	cfg.Token = Token{
		PublicKeyFile: r.String("TOKEN_PUBLIC_KEY_FILE", ""),
		KeyID:         r.String("TOKEN_KEY_ID", "visitor-key-1"),
		Issuer:        r.String("TOKEN_ISSUER", "rusuh-queue"),
		Audience:      r.String("TOKEN_AUDIENCE", "rusuh-visitor"),
	}
	cfg.Subscription = Subscription{
		TTL: r.Duration("SUBSCRIPTION_TTL", 72*time.Hour),
	}

	// Kesalahan parsing dan validasi dilaporkan bersama agar semua variable yang salah terlihat sekaligus.
	if err := errors.Join(append(parseErrs, r.Err(), cfg.Validate())...); err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	return cfg, nil
}
