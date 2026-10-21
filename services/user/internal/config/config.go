// Package config holds the User Service configuration. Nama variable mengikuti konvensi D-24
// (APP_*, LOG_*, POSTGRES_*, VALKEY_*, RABBITMQ_*) dan divalidasi saat start (fail fast).
package config

import (
	"errors"
	"fmt"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/logger"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/postgres"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/valkey"
)

// ServiceName is the canonical service name used in logs and responses.
const ServiceName = "user"

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

// Config is the full service configuration.
type Config struct {
	App      App
	Log      logger.Config
	HTTP     HTTP
	Worker   Worker
	Postgres postgres.Config
	Valkey   valkey.Config

	Token    Token
	OTP      OTP
	PII      PII
	RabbitMQ RabbitMQ
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
	errs = append(errs, c.Valkey.Validate())

	errs = append(errs, c.Token.Validate(), c.OTP.Validate(), c.PII.Validate(), c.RabbitMQ.Validate())
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("config tidak valid: %w", err)
	}
	return nil
}
