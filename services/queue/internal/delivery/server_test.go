package delivery

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/config"
)

func TestNewHandler_HealthWithoutClients(t *testing.T) {
	cfg := testConfig(t)
	mod, err := newQueueModule(cfg, &Clients{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)
	h, err := NewHandler(cfg, mod, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.NotEmpty(t, rec.Header().Get("X-Request-ID"))
	assert.Contains(t, rec.Body.String(), `"service":"queue"`)
}

// probeRegistrar mounts one route; it stands in for a domain handler of the module passed to NewHandler.
type probeRegistrar struct{}

func (probeRegistrar) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /probe", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
}

// NewHandler mounts the module it is given and builds no module of its own: the probe route exists only on the
// given module, and the config points RabbitMQ at a host that cannot be dialled, so any second dial would fail.
func TestNewHandler_UsesGivenModule(t *testing.T) {
	cfg := testConfig(t)
	cfg.RabbitMQ.URL = "amqp://guest:guest@unreachable.invalid:5672/"
	mod := &queueModule{handlers: []registrar{probeRegistrar{}}}

	h, err := NewHandler(cfg, mod, discardLogger())
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/probe", nil))
	assert.Equal(t, http.StatusTeapot, rec.Code)
}

func TestClients_EmptyIsSafe(t *testing.T) {
	c := &Clients{}
	assert.Empty(t, c.Pingers())
	c.Close()
	_ = config.ServiceName
}

// SEC-003: the composition root puts the standard security headers on every answer, the health probe included.
func TestNewHandler_SecurityHeaders(t *testing.T) {
	cfg := testConfig(t) // dibangun sebelum APP_ENV diubah: konfigurasi prod menolak bawaan uji (IMAGE_STORAGE=local)
	t.Setenv("APP_ENV", "prod")
	t.Setenv("CORS_ALLOWED_ORIGINS", "https://app.example.test")
	h, err := NewHandler(cfg, &queueModule{}, discardLogger())
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
	_, err := NewHandler(cfg, &queueModule{}, discardLogger())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "CORS_ALLOWED_ORIGINS")
}
