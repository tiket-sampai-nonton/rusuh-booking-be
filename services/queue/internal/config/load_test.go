package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setBase(t *testing.T) {
	t.Helper()
	t.Setenv("TOKEN_PRIVATE_KEY_FILE", "/run/secrets/queue-token.pem")
	t.Setenv("BOOKING_SERVICE_URL", "http://booking:8082")
	t.Setenv("PII_ENC_KEY", "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f")
	t.Setenv("PII_HMAC_KEY", "101112131415161718191a1b1c1d1e1f000102030405060708090a0b0c0d0e0f")
}

func TestLoad_Defaults(t *testing.T) {
	setBase(t)
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, 8081, cfg.App.Port)
	assert.Equal(t, "local", cfg.App.Env)
	assert.Equal(t, "queue", cfg.Postgres.Schema)
	assert.Equal(t, "svc_queue", cfg.Postgres.User)
	assert.Equal(t, "rusuh.events", cfg.RabbitMQ.Exchange)
	assert.Len(t, cfg.RabbitMQ.RetryDelays, 3)
	assert.Equal(t, 0, cfg.Valkey.DB)
}

func TestLoad_QueueExtras(t *testing.T) {
	setBase(t)
	t.Setenv("TOKEN_ADMISSION_TTL", "60s")
	t.Setenv("QUEUE_PROMOTOR_INTERVAL_MS", "100ms")
	t.Setenv("QUEUE_PROMOTE_BATCH_MAX", "200")
	t.Setenv("RATE_LIMIT_JOIN_PER_EMAIL", "5")
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "http://booking:8082", cfg.Booking.BaseURL)
	assert.Equal(t, 200, cfg.WorkerTuning.PromoteBatchMax)
	assert.Equal(t, 5, cfg.RateLimit.JoinPerEmail)
	assert.Equal(t, int64(100), cfg.WorkerTuning.PromotorInterval.Milliseconds())
	assert.Equal(t, int64(60), int64(cfg.Token.AdmissionTTL.Seconds()))
}

func TestConfigValidate_TokenPII(t *testing.T) {
	// Missing TOKEN_PRIVATE_KEY_FILE, bad PII keys, and missing BOOKING_SERVICE_URL aggregate.
	t.Setenv("PII_ENC_KEY", "00ff")
	t.Setenv("PII_HMAC_KEY", "")
	_, err := Load()
	require.Error(t, err)
	assert.ErrorContains(t, err, "TOKEN_PRIVATE_KEY_FILE")
	assert.ErrorContains(t, err, "PII_ENC_KEY")
	assert.ErrorContains(t, err, "BOOKING_SERVICE_URL")
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
		{"short pii key", "PII_ENC_KEY", "00ff", "PII_ENC_KEY"},
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
