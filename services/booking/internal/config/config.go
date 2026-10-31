// Package config holds the Booking Service configuration. Nama variable mengikuti konvensi D-24
// (APP_*, LOG_*, POSTGRES_*, VALKEY_*, RABBITMQ_*) dan divalidasi saat start (fail fast).
package config

import (
	"errors"
	"fmt"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/logger"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/postgres"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/rabbitmq"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/valkey"
)

// ServiceName is the canonical service name used in logs and responses.
const ServiceName = "booking"

// App holds APP_* variables.
type App struct {
	Env  string // local | dev | staging | prod
	Name string
	Port int
}

// HTTP holds server tuning.
type HTTP struct {
	RateLimitPerIP  int           // batas request per IP per jendela, di luar /health & /metrics (0 = nonaktif)
	RateLimitWindow time.Duration // panjang jendela batas di atas
	ShutdownTimeout time.Duration
	HealthTimeout   time.Duration // batas waktu ping semua dependency di GET /health
}

// Worker holds background job tuning.
type Worker struct {
	Interval           time.Duration
	VerifyGraceSeconds int // VERIFY_GRACE_SECONDS: how long VERIFYING bookings wait before forced expiry (design §7.2)
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
	Image    Image
}

// Image configures where uploaded event images are stored. Storage is "local" (a git-ignored
// directory served under /media) or "s3" (a bucket fronted by PublicBaseURL, e.g. CloudFront).
type Image struct {
	Storage       string
	LocalDir      string
	S3Bucket      string
	S3Endpoint    string // opsional: S3-compatible endpoint (pengujian / LocalStack)
	PublicBaseURL string // awalan URL publik gambar, tanpa slash akhir
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
	if c.HTTP.ShutdownTimeout <= 0 || c.HTTP.HealthTimeout <= 0 || c.Worker.Interval <= 0 || c.HTTP.RateLimitWindow <= 0 {
		errs = append(errs, errors.New("HTTP_SHUTDOWN_TIMEOUT, HEALTH_TIMEOUT, WORKER_INTERVAL, dan RATE_LIMIT_WINDOW harus > 0"))
	}
	if c.Worker.VerifyGraceSeconds < 60 || c.Worker.VerifyGraceSeconds > 600 {
		errs = append(errs, fmt.Errorf("VERIFY_GRACE_SECONDS %d di luar rentang [60, 600]", c.Worker.VerifyGraceSeconds))
	}
	switch c.Image.Storage {
	case "local":
		if c.App.Env != "local" {
			errs = append(errs, errors.New("IMAGE_STORAGE=local hanya untuk APP_ENV=local; pakai s3"))
		}
		if c.Image.LocalDir == "" {
			errs = append(errs, errors.New("IMAGE_LOCAL_DIR wajib untuk IMAGE_STORAGE=local"))
		}
	case "s3":
		if c.Image.S3Bucket == "" || c.Image.PublicBaseURL == "" {
			errs = append(errs, errors.New("IMAGE_S3_BUCKET dan IMAGE_PUBLIC_BASE_URL wajib untuk IMAGE_STORAGE=s3"))
		}
	default:
		errs = append(errs, fmt.Errorf("IMAGE_STORAGE %q tidak dikenal (local | s3)", c.Image.Storage))
	}
	errs = append(errs, c.Log.Validate())
	errs = append(errs, c.Postgres.Validate())
	errs = append(errs, c.Valkey.Validate())
	errs = append(errs, c.RabbitMQ.Validate())
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("config tidak valid: %w", err)
	}
	return nil
}
