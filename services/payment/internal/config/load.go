package config

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/dotenv"
)

// Load reads configuration from the environment (and an optional .env file in the working directory).
func Load() (Config, error) {
	if err := dotenv.Load(".env"); err != nil {
		return Config{}, err
	}

	port, err := dotenv.Int("SERVICE_PORT", 8083)
	if err != nil {
		return Config{}, err
	}
	dialTimeout, err := dotenv.Duration("DEPENDENCY_DIAL_TIMEOUT", 500*time.Millisecond)
	if err != nil {
		return Config{}, err
	}
	shutdownTimeout, err := dotenv.Duration("SHUTDOWN_TIMEOUT", 15*time.Second)
	if err != nil {
		return Config{}, err
	}
	workerInterval, err := dotenv.Duration("WORKER_INTERVAL", 30*time.Second)
	if err != nil {
		return Config{}, err
	}
	var level slog.Level
	if err := level.UnmarshalText([]byte(dotenv.String("LOG_LEVEL", "INFO"))); err != nil {
		return Config{}, fmt.Errorf("config: LOG_LEVEL: %w", err)
	}

	return Config{
		ServiceName:     ServiceName,
		Port:            port,
		LogLevel:        level,
		DialTimeout:     dialTimeout,
		ShutdownTimeout: shutdownTimeout,
		WorkerInterval:  workerInterval,
		Dependencies: []Dependency{
			{Name: "postgres", Addr: dotenv.String("POSTGRES_ADDR", "localhost:5432")},
			{Name: "rabbitmq", Addr: dotenv.String("RABBITMQ_ADDR", "localhost:5672")},
		},
	}, nil
}
