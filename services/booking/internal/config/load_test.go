package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setBase(t *testing.T) {
	t.Helper()
}

func TestLoad_Defaults(t *testing.T) {
	setBase(t)
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, 8082, cfg.App.Port)
	assert.Equal(t, "local", cfg.App.Env)
	assert.Equal(t, "booking", cfg.Postgres.Schema)
	assert.Equal(t, "svc_booking", cfg.Postgres.User)
	assert.Equal(t, "rusuh.events", cfg.RabbitMQ.Exchange)
	assert.Len(t, cfg.RabbitMQ.RetryDelays, 3)
	assert.Equal(t, 0, cfg.Valkey.DB)
}

func TestLoad_ReportsInvalidValues(t *testing.T) {
	tests := []struct {
		name, key, value, want string
	}{
		{"bad port", "APP_PORT", "eighty", "APP_PORT"},
		{"port range", "APP_PORT", "70000", "APP_PORT"},
		{"bad env", "APP_ENV", "qa", "APP_ENV"},
		{"bad level", "LOG_LEVEL", "verbose", "LOG_LEVEL"},
		{"bad timeout", "HEALTH_TIMEOUT", "0s", "HEALTH_TIMEOUT"},
		{"bad schema", "POSTGRES_SCHEMA", "public;drop", "POSTGRES_SCHEMA"},
		{"bad valkey", "VALKEY_SERVER", "localhost:6379", "VALKEY_SERVER"},
		{"bad delays", "RABBITMQ_RETRY_DELAYS", "soon", "RABBITMQ_RETRY_DELAYS"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setBase(t)
			t.Setenv(tt.key, tt.value)
			_, err := Load()
			assert.ErrorContains(t, err, tt.want)
		})
	}
}

func TestLoad_RateLimitFromEnv(t *testing.T) {
	setBase(t)
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, 300, cfg.HTTP.RateLimitPerIP)
	assert.Equal(t, time.Minute, cfg.HTTP.RateLimitWindow)

	t.Setenv("RATE_LIMIT_PER_IP", "7")
	t.Setenv("RATE_LIMIT_WINDOW", "10s")
	cfg, err = Load()
	require.NoError(t, err)
	assert.Equal(t, 7, cfg.HTTP.RateLimitPerIP)
	assert.Equal(t, 10*time.Second, cfg.HTTP.RateLimitWindow)

	t.Setenv("RATE_LIMIT_WINDOW", "0s")
	_, err = Load()
	assert.ErrorContains(t, err, "RATE_LIMIT_WINDOW")
}

func TestLoad_ImageStorage(t *testing.T) {
	setBase(t)
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, Image{Storage: "local", LocalDir: "var/images", PublicBaseURL: "http://localhost:8082/media"}, cfg.Image)

	t.Setenv("IMAGE_STORAGE", "s3")
	_, err = Load()
	assert.ErrorContains(t, err, "IMAGE_S3_BUCKET")

	t.Setenv("IMAGE_S3_BUCKET", "bkt")
	t.Setenv("IMAGE_PUBLIC_BASE_URL", "https://cdn.example/")
	cfg, err = Load()
	require.NoError(t, err)
	assert.Equal(t, "https://cdn.example", cfg.Image.PublicBaseURL)

	t.Setenv("IMAGE_STORAGE", "ftp")
	_, err = Load()
	assert.ErrorContains(t, err, "IMAGE_STORAGE")

	t.Setenv("IMAGE_STORAGE", "local")
	t.Setenv("APP_ENV", "staging")
	_, err = Load()
	assert.ErrorContains(t, err, "hanya untuk APP_ENV=local")
}
