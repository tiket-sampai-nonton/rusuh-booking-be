// Package logger builds the structured logger (log/slog) used by every service.
package logger

import (
	"fmt"
	"io"
	"log/slog"
	"strings"
)

// Config is the logger configuration (LOG_LEVEL, LOG_FORMAT).
type Config struct {
	Level  string // debug | info | warn | error
	Format string // json | text
}

// Validate rejects unknown levels and formats so a typo fails at start-up, not in production.
func (c Config) Validate() error {
	if _, err := parseLevel(c.Level); err != nil {
		return err
	}
	switch strings.ToLower(c.Format) {
	case "", "json", "text":
		return nil
	default:
		return fmt.Errorf("logger: LOG_FORMAT %q tidak dikenal (json | text)", c.Format)
	}
}

// New returns a logger writing to w with the static attributes service and verb.
func New(w io.Writer, cfg Config, service, verb string) (*slog.Logger, error) {
	level, err := parseLevel(cfg.Level)
	if err != nil {
		return nil, err
	}
	opts := &slog.HandlerOptions{Level: level}
	var h slog.Handler
	if strings.EqualFold(cfg.Format, "text") {
		h = slog.NewTextHandler(w, opts)
	} else {
		h = slog.NewJSONHandler(w, opts)
	}
	return slog.New(h).With("service", service, "verb", verb), nil
}

func parseLevel(s string) (slog.Level, error) {
	if s == "" {
		return slog.LevelInfo, nil
	}
	var l slog.Level
	if err := l.UnmarshalText([]byte(strings.ToUpper(s))); err != nil {
		return 0, fmt.Errorf("logger: LOG_LEVEL %q tidak dikenal (debug | info | warn | error)", s)
	}
	return l, nil
}
