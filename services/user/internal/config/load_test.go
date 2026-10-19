package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setBase(t *testing.T) {
	t.Helper()

	t.Setenv("TOKEN_PRIVATE_KEY_FILE", "/run/secrets/user-token.pem")
	t.Setenv("PII_ENC_KEY", "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f")
	t.Setenv("PII_HASH_KEY", "101112131415161718191a1b1c1d1e1f000102030405060708090a0b0c0d0e0f")
	t.Setenv("OTP_HASH_KEY", "202122232425262728292a2b2c2d2e2f000102030405060708090a0b0c0d0e0f")
}

func TestLoad_Defaults(t *testing.T) {
	setBase(t)
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, 8085, cfg.App.Port)
	assert.Equal(t, "local", cfg.App.Env)
	assert.Equal(t, "users", cfg.Postgres.Schema)
	assert.Equal(t, "svc_user", cfg.Postgres.User)
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
		{"short pii key", "PII_ENC_KEY", "00ff", "PII_ENC_KEY"},
		{"otp ttl", "OTP_TTL", "1h", "OTP_TTL"},
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
