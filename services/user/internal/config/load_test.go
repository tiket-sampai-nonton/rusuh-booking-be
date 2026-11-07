package config

import (
	"testing"
	"time"

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

func TestLoad_HardeningValidation(t *testing.T) {
	const (
		keyA = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"
		keyB = "101112131415161718191a1b1c1d1e1f000102030405060708090a0b0c0d0e0f"
		keyC = "202122232425262728292a2b2c2d2e2f000102030405060708090a0b0c0d0e0f"
	)
	tests := []struct {
		name string
		env  map[string]string
		want string // "" = harus lolos
	}{
		{"attempts 2", map[string]string{"OTP_MAX_ATTEMPTS": "2"}, "OTP_MAX_ATTEMPTS"},
		{"attempts 11", map[string]string{"OTP_MAX_ATTEMPTS": "11"}, "OTP_MAX_ATTEMPTS"},
		{"per email 0", map[string]string{"OTP_MAX_PER_EMAIL_HOUR": "0"}, "OTP_MAX_PER_EMAIL_HOUR"},
		{"per email 11", map[string]string{"OTP_MAX_PER_EMAIL_HOUR": "11", "OTP_MAX_PER_IP_HOUR": "50"}, "OTP_MAX_PER_EMAIL_HOUR"},
		{"per ip below email", map[string]string{"OTP_MAX_PER_EMAIL_HOUR": "5", "OTP_MAX_PER_IP_HOUR": "4"}, "OTP_MAX_PER_IP_HOUR"},
		{"per ip above 1000", map[string]string{"OTP_MAX_PER_IP_HOUR": "1001"}, "OTP_MAX_PER_IP_HOUR"},
		{"max pair 10x10 at budget", map[string]string{"OTP_MAX_ATTEMPTS": "10", "OTP_MAX_PER_EMAIL_HOUR": "10", "OTP_MAX_PER_IP_HOUR": "20"}, ""},
		{"enc==otp", map[string]string{"OTP_HASH_KEY": keyA}, "PII_ENC_KEY dan OTP_HASH_KEY"},
		{"enc==hash", map[string]string{"PII_HASH_KEY": keyA}, "PII_ENC_KEY dan PII_HASH_KEY"},
		{"hash==otp", map[string]string{"OTP_HASH_KEY": keyB}, "PII_HASH_KEY dan OTP_HASH_KEY"},
		{"enc==otp uppercase", map[string]string{"OTP_HASH_KEY": "000102030405060708090A0B0C0D0E0F101112131415161718191A1B1C1D1E1F"}, "PII_ENC_KEY dan OTP_HASH_KEY"},
		{"hops default ok", nil, ""},
		{"hops 0", map[string]string{"APP_TRUSTED_PROXY_HOPS": "0"}, "APP_TRUSTED_PROXY_HOPS"},
		{"hops 6", map[string]string{"APP_TRUSTED_PROXY_HOPS": "6"}, "APP_TRUSTED_PROXY_HOPS"},
		{"hops 2 ok", map[string]string{"APP_TRUSTED_PROXY_HOPS": "2"}, ""},
		{"verify per ip 0", map[string]string{"OTP_MAX_VERIFY_PER_IP_MIN": "0"}, "OTP_MAX_VERIFY_PER_IP_MIN"},
		{"verify per ip 6001", map[string]string{"OTP_MAX_VERIFY_PER_IP_MIN": "6001"}, "OTP_MAX_VERIFY_PER_IP_MIN"},
		{"session per ip 0", map[string]string{"TOKEN_MAX_REFRESH_PER_IP_MIN": "0"}, "TOKEN_MAX_REFRESH_PER_IP_MIN"},
		{"session per ip 6001", map[string]string{"TOKEN_MAX_REFRESH_PER_IP_MIN": "6001"}, "TOKEN_MAX_REFRESH_PER_IP_MIN"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setBase(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			_, err := Load()
			if tt.want == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.ErrorContains(t, err, tt.want)
			for _, k := range []string{keyA, keyB, keyC} {
				assert.NotContains(t, err.Error(), k, "error must not echo key bytes")
			}
		})
	}
}

func TestLoad_HardeningDefaults(t *testing.T) {
	setBase(t)
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, 1, cfg.App.TrustedProxyHops)
	assert.Equal(t, 60, cfg.OTP.MaxVerifyPerIPMinute)
	assert.Equal(t, 300, cfg.Token.MaxRefreshPerIPMinute)
}

// Pasangan terbesar yang diizinkan (10×10) tepat di batas anggaran tebakan 100.
func TestOTPValidate_GuessBudgetBoundary(t *testing.T) {
	o := OTP{TTL: 5 * time.Minute, ResendCooldown: time.Minute, MaxAttempts: 10, MaxPerEmailHour: 10, MaxPerIPHour: 20, MaxVerifyPerIPMinute: 60}
	require.NoError(t, o.Validate())
}
