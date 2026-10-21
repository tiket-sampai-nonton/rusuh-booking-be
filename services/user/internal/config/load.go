package config

import (
	"errors"
	"fmt"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/dotenv"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/logger"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/postgres"
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
			Port: r.Int("APP_PORT", 8085),
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
		User:     r.String("POSTGRES_USER", "svc_user"),
		Password: r.String("POSTGRES_PASSWORD", ""),
		Database: r.String("POSTGRES_DATABASE", "rbt"),
		SSLMode:  r.String("POSTGRES_SSLMODE", "disable"),
		Schema:   r.String("POSTGRES_SCHEMA", "users"),
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

	cfg.Token = Token{
		Issuer:         r.String("TOKEN_ISSUER", "rbt-user"),
		Audience:       r.String("TOKEN_AUDIENCE", "rbt"),
		KeyID:          r.String("TOKEN_KEY_ID", "user-1"),
		PrivateKeyFile: r.String("TOKEN_PRIVATE_KEY_FILE", ""),
		AccessTTL:      r.Duration("TOKEN_ACCESS_TTL", 15*time.Minute),
		RefreshTTL:     r.Duration("TOKEN_REFRESH_TTL", 30*24*time.Hour),
	}
	cfg.OTP = OTP{
		TTL:             r.Duration("OTP_TTL", 5*time.Minute),
		ResendCooldown:  r.Duration("OTP_RESEND_COOLDOWN", 60*time.Second),
		MaxAttempts:     r.Int("OTP_MAX_ATTEMPTS", 5),
		MaxPerEmailHour: r.Int("OTP_MAX_PER_EMAIL_HOUR", 5),
		MaxPerIPHour:    r.Int("OTP_MAX_PER_IP_HOUR", 20),
	}
	cfg.PII = PII{
		EncKeyHex:  r.String("PII_ENC_KEY", ""),
		HashKeyHex: r.String("PII_HASH_KEY", ""),
		OTPKeyHex:  r.String("OTP_HASH_KEY", ""),
	}
	cfg.RabbitMQ = RabbitMQ{
		URL:         r.String("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/"),
		Exchange:    r.String("RABBITMQ_EXCHANGE", "rusuh.events"),
		ConfirmWait: r.Duration("RABBITMQ_CONFIRM_TIMEOUT", 5*time.Second),
	}

	// Kesalahan parsing dan validasi dilaporkan bersama agar semua variable yang salah terlihat sekaligus.
	if err := errors.Join(append(parseErrs, r.Err(), cfg.Validate())...); err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	return cfg, nil
}
