// Package rabbitmq provides the publisher (publisher confirm) and consumer (manual ack,
// retry berjenjang, DLQ) used by RBT services. Topologi mengikuti SDD §4.6.2.
package rabbitmq

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Config holds the RABBITMQ_* variables.
type Config struct {
	URL          string          // RABBITMQ_URL
	Exchange     string          // RABBITMQ_EXCHANGE (default rusuh.events)
	ExchangeType string          // RABBITMQ_EXCHANGE_TYPE (default topic)
	Workers      int             // RABBITMQ_WORKERS: goroutine per consumer queue
	Prefetch     int             // RABBITMQ_PREFETCH
	MaxRetries   int             // RABBITMQ_MAX_RETRIES: percobaan ulang sebelum DLQ (default 3 → total 4 percobaan)
	RetryDelays  []time.Duration // RABBITMQ_RETRY_DELAYS: mis. 5s,30s,2m
	ConfirmWait  time.Duration   // RABBITMQ_CONFIRM_TIMEOUT
}

// ParseDelays parses "5s,30s,2m".
func ParseDelays(s string) ([]time.Duration, error) {
	var out []time.Duration
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		d, err := time.ParseDuration(part)
		if err != nil {
			return nil, fmt.Errorf("rabbitmq: RABBITMQ_RETRY_DELAYS %q: %w", part, err)
		}
		out = append(out, d)
	}
	return out, nil
}

// Validate fails fast on malformed configuration.
func (c Config) Validate() error {
	var errs []error
	u, err := url.Parse(c.URL)
	if err != nil || (u.Scheme != "amqp" && u.Scheme != "amqps") || u.Host == "" {
		errs = append(errs, errors.New("rabbitmq: RABBITMQ_URL harus amqp:// atau amqps:// dengan host"))
	}
	if c.Exchange == "" {
		errs = append(errs, errors.New("rabbitmq: RABBITMQ_EXCHANGE wajib diisi"))
	}
	switch c.ExchangeType {
	case "topic", "direct", "fanout":
	default:
		errs = append(errs, fmt.Errorf("rabbitmq: RABBITMQ_EXCHANGE_TYPE %q tidak dikenal", c.ExchangeType))
	}
	if c.Workers < 1 || c.Workers > 64 {
		errs = append(errs, fmt.Errorf("rabbitmq: RABBITMQ_WORKERS %d di luar rentang 1–64", c.Workers))
	}
	if c.Prefetch < 1 || c.Prefetch > 1000 {
		errs = append(errs, fmt.Errorf("rabbitmq: RABBITMQ_PREFETCH %d di luar rentang 1–1000", c.Prefetch))
	}
	if c.MaxRetries < 0 || c.MaxRetries > 10 {
		errs = append(errs, fmt.Errorf("rabbitmq: RABBITMQ_MAX_RETRIES %d di luar rentang 0–10", c.MaxRetries))
	}
	if c.MaxRetries > 0 && len(c.RetryDelays) == 0 {
		errs = append(errs, errors.New("rabbitmq: RABBITMQ_RETRY_DELAYS wajib diisi bila RABBITMQ_MAX_RETRIES > 0"))
	}
	if c.ConfirmWait <= 0 {
		errs = append(errs, errors.New("rabbitmq: RABBITMQ_CONFIRM_TIMEOUT harus > 0"))
	}
	return errors.Join(errs...)
}

// Redacted returns the URL without credentials, for logs.
func (c Config) Redacted() string {
	u, err := url.Parse(c.URL)
	if err != nil {
		return "invalid-url"
	}
	return u.Redacted()
}
