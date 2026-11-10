// Package config holds the Queue Service configuration. Nama variable mengikuti konvensi D-24
// (APP_*, LOG_*, POSTGRES_*, VALKEY_*, RABBITMQ_*) dan divalidasi saat start (fail fast).
package config

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/logger"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/postgres"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/rabbitmq"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/valkey"
)

// ServiceName is the canonical service name used in logs and responses.
const ServiceName = "queue"

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
	RabbitMQ rabbitmq.Config

	Token        Token
	PII          PII
	Booking      Booking
	RateLimit    RateLimit
	WorkerTuning WorkerTuning
	Liveness     Liveness
	Admin        Admin
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
	if c.HTTP.ShutdownTimeout <= 0 {
		errs = append(errs, errors.New("HTTP_SHUTDOWN_TIMEOUT harus > 0"))
	}
	if c.HTTP.HealthTimeout <= 0 {
		errs = append(errs, errors.New("HEALTH_TIMEOUT harus > 0"))
	}
	if c.Worker.Interval <= 0 {
		errs = append(errs, errors.New("WORKER_INTERVAL harus > 0"))
	}
	errs = append(errs, validatePageBaseURL(c.Booking.PageBaseURL, c.App.Env))
	errs = append(errs, c.Log.Validate())
	errs = append(errs, c.Postgres.Validate())
	errs = append(errs, c.Valkey.Validate())
	errs = append(errs, c.RabbitMQ.Validate())
	errs = append(errs, c.Token.Validate(), c.PII.Validate(), c.Booking.Validate())
	errs = append(errs, c.RateLimit.Validate(), c.WorkerTuning.Validate(c.Booking.Timeout), c.Liveness.Validate())
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("config tidak valid: %w", err)
	}
	return nil
}

// validatePageBaseURL checks BOOKING_PAGE_BASE_URL: absolute http(s) URL with a host, no userinfo, query or fragment,
// and https outside APP_ENV=local. Errors name the variable and the rule only, never other config values.
func validatePageBaseURL(raw, env string) error {
	const name = "BOOKING_PAGE_BASE_URL"
	if raw == "" {
		return errors.New(name + " wajib diisi")
	}
	if strings.IndexFunc(raw, func(r rune) bool { return r <= ' ' || r == 0x7f }) >= 0 {
		return errors.New(name + " tidak boleh mengandung spasi atau karakter kontrol")
	}
	if strings.Contains(raw, "?") {
		return errors.New(name + " tidak boleh mengandung query")
	}
	if strings.Contains(raw, "#") {
		return errors.New(name + " tidak boleh mengandung fragment")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" {
		return errors.New(name + " harus URL absolut (http/https)")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New(name + " harus memakai skema http atau https")
	}
	if u.Hostname() == "" {
		return errors.New(name + " harus punya host")
	}
	if u.User != nil {
		return errors.New(name + " tidak boleh mengandung userinfo")
	}
	if u.Scheme == "http" && env != "local" {
		return errors.New(name + " harus https kecuali APP_ENV=local")
	}
	return nil
}
