package delivery

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

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
