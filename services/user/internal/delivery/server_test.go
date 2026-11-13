package delivery

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/config"
)

func TestNewHandler_HealthWithoutClients(t *testing.T) {
	cfg := testConfig(t)
	h, err := NewHandler(cfg, &Clients{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.NotEmpty(t, rec.Header().Get("X-Request-ID"))
	assert.Contains(t, rec.Body.String(), `"service":"user"`)
}

func TestClients_EmptyIsSafe(t *testing.T) {
	c := &Clients{}
	assert.Empty(t, c.Pingers())
	assert.NoError(t, c.Close())
	_ = config.ServiceName
}

// P-16: closers run exactly once, in reverse registration order, errors joined, with names only.
func TestClients_ClosersRunOnceInReverseOrderAndJoinErrors(t *testing.T) {
	c := &Clients{}
	var order []string
	c.AddCloser("first", func() error { order = append(order, "first"); return nil })
	c.AddCloser("second", func() error { order = append(order, "second"); return errors.New("boom-2") })
	c.AddCloser("third", func() error { order = append(order, "third"); return errors.New("boom-3") })

	err := c.Close()
	assert.Equal(t, []string{"third", "second", "first"}, order)
	require.Error(t, err)
	assert.ErrorContains(t, err, "tutup third: boom-3")
	assert.ErrorContains(t, err, "tutup second: boom-2")

	assert.NoError(t, c.Close(), "panggilan kedua tidak menjalankan closer lagi")
	assert.Equal(t, 3, len(order))
}

func TestClients_CloseWithoutClosersIsNil(t *testing.T) {
	assert.NoError(t, (&Clients{}).Close())
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
