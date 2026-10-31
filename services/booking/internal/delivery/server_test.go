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

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/config"
)

func TestNewHandler_HealthWithoutClients(t *testing.T) {
	cfg := testConfig(t)
	h, err := NewHandler(cfg, &Clients{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.NotEmpty(t, rec.Header().Get("X-Request-ID"))
	assert.Contains(t, rec.Body.String(), `"service":"booking"`)
}

func TestClients_EmptyIsSafe(t *testing.T) {
	c := &Clients{}
	assert.Empty(t, c.Pingers())
	c.Close()
	_ = config.ServiceName
}

func TestNewHandler_RateLimitsPerIPButNotHealth(t *testing.T) {
	cfg := testConfig(t)
	cfg.HTTP.RateLimitWindow = time.Minute
	cfg.HTTP.RateLimitPerIP = 1
	cfg.HTTP.RateLimitPerIP = 1
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
	assert.Equal(t, http.StatusNotFound, do(http.MethodGet, "/api/v1/unknown"))
	assert.Equal(t, http.StatusTooManyRequests, do(http.MethodGet, "/api/v1/unknown"))
}
