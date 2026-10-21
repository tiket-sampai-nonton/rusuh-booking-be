// Package config holds the Notification Service configuration. Nama variable mengikuti konvensi D-24
// (APP_*, LOG_*, POSTGRES_*, VALKEY_*, RABBITMQ_*) dan divalidasi saat start (fail fast).
package config

import (
	"errors"
	"fmt"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/logger"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/postgres"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/rabbitmq"
)

// ServiceName is the canonical service name used in logs and responses.
const ServiceName = "notification"

// App holds APP_* variables.
type App struct {
	Env  string // local | dev | staging | prod
	Name string
	Port int
}

// HTTP holds server tuning.
type HTTP struct {
	ShutdownTimeout time.Duration
	HealthTimeout   time.Duration // batas waktu ping semua dependency di GET /health
}

// Worker holds background job tuning.
type Worker struct {
	Interval time.Duration
}

// SMTP holds the email sender settings (SES / Mailpit).
type SMTP struct {
	Host     string
	Port     int
	User     string
	Password string
	From     string
	Timeout  time.Duration
}

// FCM holds the push sender settings (FCM HTTP v1).
type FCM struct {
	ProjectID       string
	CredentialsPath string
}

// PII holds the field-level protection keys (hex, 32 bytes each). EncKeyHex is shared with the
// User Service so otp.requested ciphertext opens here.
type PII struct {
	EncKeyHex  string
	HashKeyHex string
}

// Token holds the visitor-token verification settings (public key minted by Queue Service).
type Token struct {
	PublicKeyFile string
	KeyID         string
	Issuer        string
	Audience      string
}

// Subscription holds push-subscription policy.
type Subscription struct {
	TTL time.Duration
}

// Config is the full service configuration.
type Config struct {
	App          App
	Log          logger.Config
	HTTP         HTTP
	Worker       Worker
	Postgres     postgres.Config
	RabbitMQ     rabbitmq.Config
	SMTP         SMTP
	FCM          FCM
	PII          PII
	Token        Token
	Subscription Subscription
}

// Validate checks cross-field rules.
func (c Config) Validate() error {
	var errs []error
	switch c.App.Env {
	case "local", "dev", "staging", "prod":
	default:
		errs = append(errs, fmt.Errorf("APP_ENV %q tidak dikenal (local | dev | staging | prod)", c.App.Env))
	}
	if c.App.Port <= 0 || c.App.Port > 65535 {
		errs = append(errs, fmt.Errorf("APP_PORT %d di luar rentang", c.App.Port))
	}
	if c.HTTP.ShutdownTimeout <= 0 || c.HTTP.HealthTimeout <= 0 || c.Worker.Interval <= 0 {
		errs = append(errs, errors.New("HTTP_SHUTDOWN_TIMEOUT, HEALTH_TIMEOUT, dan WORKER_INTERVAL harus > 0"))
	}
	errs = append(errs, c.Log.Validate())
	errs = append(errs, c.Postgres.Validate())
	errs = append(errs, c.RabbitMQ.Validate())
	// SMTP/FCM/PII/Token are only needed by the worker; they are parsed and validated in the module
	// wiring (newNotificationModule) so the api verb and /health unit tests boot without them.
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("config tidak valid: %w", err)
	}
	return nil
}
