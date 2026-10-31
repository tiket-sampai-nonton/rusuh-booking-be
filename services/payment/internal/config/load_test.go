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
	assert.Equal(t, 8083, cfg.App.Port)
	assert.Equal(t, "local", cfg.App.Env)
	assert.Equal(t, "payment", cfg.Postgres.Schema)
	assert.Equal(t, "svc_payment", cfg.Postgres.User)
	assert.Equal(t, "rusuh.events", cfg.RabbitMQ.Exchange)
	assert.Len(t, cfg.RabbitMQ.RetryDelays, 3)
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

func TestLoad_PaymentDefaultsAndRanges(t *testing.T) {
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "https://api.sandbox.midtrans.com", cfg.Payment.Midtrans.BaseURL)
	assert.Equal(t, "https://app.sandbox.midtrans.com", cfg.Payment.Midtrans.SnapBaseURL)
	assert.Equal(t, []string{"VA_BCA", "VA_BNI", "VA_BRI", "QRIS", "GOPAY"}, cfg.Payment.Methods)
	assert.Equal(t, 60*time.Second, cfg.Payment.PageTokenTTL)
	assert.Equal(t, 5, cfg.Payment.Midtrans.Charge.FailureThreshold)
	assert.Equal(t, 20, cfg.Payment.Midtrans.Charge.Bulkhead)

	t.Setenv("MIDTRANS_ENVIRONMENT", "production")
	cfg, err = Load()
	require.NoError(t, err)
	assert.Equal(t, "https://api.midtrans.com", cfg.Payment.Midtrans.BaseURL)
	assert.Equal(t, "https://app.midtrans.com", cfg.Payment.Midtrans.SnapBaseURL)
}

func TestLoad_ReportsInvalidPaymentValues(t *testing.T) {
	tests := []struct{ key, value, want string }{
		{"MIDTRANS_ENVIRONMENT", "staging", "MIDTRANS_ENVIRONMENT"},
		{"MIDTRANS_BASE_URL", "ftp://x", "MIDTRANS_BASE_URL"},
		{"BOOKING_SERVICE_URL", "booking", "BOOKING_SERVICE_URL"},
		{"PAYMENT_METHODS_ENABLED", "VA_BCA,KARTU", "KARTU"},
		{"PAYMENT_METHODS_ENABLED", " , ", "PAYMENT_METHODS_ENABLED"},
		{"PAYMENT_PAGE_TOKEN_TTL_SECONDS", "10", "PAYMENT_PAGE_TOKEN_TTL_SECONDS"},
		{"PAYMENT_PAGE_TOKEN_TTL_SECONDS", "300", "PAYMENT_PAGE_TOKEN_TTL_SECONDS"},
		{"MIDTRANS_CHARGE_FAILURE_THRESHOLD", "0", "MIDTRANS_CHARGE"},
		{"MIDTRANS_STATUS_BULKHEAD", "0", "MIDTRANS_STATUS"},
		{"MIDTRANS_CHARGE_COOLDOWN", "0s", "MIDTRANS_CHARGE"},
		{"MIDTRANS_NOTIFICATION_URL", "http://tidak-aman.example", "https"},
		{"MIDTRANS_NOTIFICATION_URL", "bukan-url", "MIDTRANS_NOTIFICATION_URL"},
	}
	for _, tt := range tests {
		t.Run(tt.key+"="+tt.value, func(t *testing.T) {
			t.Setenv(tt.key, tt.value)
			_, err := Load()
			assert.ErrorContains(t, err, tt.want)
		})
	}
}

func TestPayment_ValidateForServeRequiresSecretsAndKeys(t *testing.T) {
	cfg, err := Load()
	require.NoError(t, err, "migrate dan healthcheck tidak butuh secret")

	err = cfg.Payment.ValidateForServe()
	assert.ErrorContains(t, err, "MIDTRANS_SERVER_KEY")
	assert.ErrorContains(t, err, "TOKEN_PUBLIC_KEY_FILE")
	assert.ErrorContains(t, err, "SERVICE_TOKEN_PRIVATE_KEY_FILE")
	assert.ErrorContains(t, err, "BOOKING_TOKEN_PUBLIC_KEY_FILE")

	p := cfg.Payment
	p.Midtrans.ServerKey = "SB-Mid-server-test"
	p.Token.VisitorPublicKeyFile, p.Token.ServicePrivateKeyFile, p.Token.BookingPublicKeyFile = "a", "b", "c"
	assert.NoError(t, p.ValidateForServe())
}
