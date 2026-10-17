// Package config holds the Queue Service configuration.
package config

import (
	"log/slog"
	"time"
)

// ServiceName is the canonical service name used in logs and responses.
const ServiceName = "queue"

// Dependency is an infrastructure endpoint checked by /readyz.
type Dependency struct {
	Name string
	Addr string
}

// Config is the full service configuration.
type Config struct {
	ServiceName     string
	Port            int
	LogLevel        slog.Level
	DialTimeout     time.Duration
	ShutdownTimeout time.Duration
	WorkerInterval  time.Duration
	Dependencies    []Dependency
}
