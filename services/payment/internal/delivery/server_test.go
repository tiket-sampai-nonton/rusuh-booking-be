package delivery

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/config"
)

func TestNewHandler_HealthWithoutClients(t *testing.T) {
	cfg := testConfig(t)
	h, err := NewHandler(cfg, &Clients{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.NotEmpty(t, rec.Header().Get("X-Request-ID"))
	assert.Contains(t, rec.Body.String(), `"service":"payment"`)
}

func TestClients_EmptyIsSafe(t *testing.T) {
	c := &Clients{}
	assert.Empty(t, c.Pingers())
	c.Close()
	_ = config.ServiceName
}

func TestNewAPIHandler_FailsFastWithoutSecretsOrKeys(t *testing.T) {
	cfg := testConfig(t) // Load tidak butuh rahasia; api/worker membutuhkannya
	_, err := NewAPIHandler(cfg, &Clients{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.Error(t, err)
	assert.ErrorContains(t, err, "MIDTRANS_SERVER_KEY")
	assert.ErrorContains(t, err, "SERVICE_TOKEN_PRIVATE_KEY_FILE")

	cfg.Payment.Midtrans.ServerKey = "SB-Mid-server-x"
	cfg.Payment.Token.VisitorPublicKeyFile = "/tidak/ada.pem"
	cfg.Payment.Token.ServicePrivateKeyFile = "/tidak/ada.pem"
	cfg.Payment.Token.BookingPublicKeyFile = "/tidak/ada.pem"
	_, err = NewAPIHandler(cfg, &Clients{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.Error(t, err)
	assert.ErrorContains(t, err, "baca kunci", "file kunci yang hilang menghentikan start")
	assert.NotContains(t, err.Error(), "SB-Mid-server-x", "rahasia tidak pernah muncul di error")
}

func TestNewHandler_RateLimitsPerIPButNotHealth(t *testing.T) {
	cfg := testConfig(t)
	cfg.HTTP.RateLimitWindow = time.Minute
	cfg.HTTP.RateLimitPerIP = 1
	cfg.HTTP.RateLimitWebhookPerIP = 1
	h, err := NewHandler(cfg, &Clients{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)
	do := func(method, path string) int {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
		return rec.Code
	}
	for i := 0; i < 3; i++ {
		assert.Equal(t, http.StatusOK, do(http.MethodGet, "/health"), "health tidak dibatasi")
	}
	assert.Equal(t, http.StatusNotFound, do(http.MethodPost, "/api/v1/webhooks/midtrans"))
	assert.Equal(t, http.StatusTooManyRequests, do(http.MethodPost, "/api/v1/webhooks/midtrans"))
}

// SEC-003: the composition root puts the standard security headers on every answer, the health probe included.
func TestNewHandler_SecurityHeaders(t *testing.T) {
	cfg := testConfig(t) // dibangun sebelum APP_ENV diubah: konfigurasi prod menolak bawaan uji (IMAGE_STORAGE=local)
	t.Setenv("APP_ENV", "prod")
	t.Setenv("CORS_ALLOWED_ORIGINS", "https://app.example.test")
	h, err := NewHandler(cfg, &Clients{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "no-referrer", rec.Header().Get("Referrer-Policy"))
	assert.Equal(t, "max-age=31536000", rec.Header().Get("Strict-Transport-Security"))
}

// SEC-004: a deployed service (APP_ENV not local) refuses to build its handler without a CORS allowlist.
func TestNewHandler_RefusesDeployedEnvWithoutCORSAllowlist(t *testing.T) {
	cfg := testConfig(t)
	t.Setenv("APP_ENV", "prod")
	t.Setenv("CORS_ALLOWED_ORIGINS", "")
	_, err := NewHandler(cfg, &Clients{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "CORS_ALLOWED_ORIGINS")
}
