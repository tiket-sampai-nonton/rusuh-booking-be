package httpx_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/httpx"
)

// SEC-003: every API answer carries the standard security headers; HSTS only outside local (plain HTTP local dev).
func TestSecurityHeaders(t *testing.T) {
	tests := []struct {
		name     string
		appEnv   string
		wantHSTS string
	}{
		{"local has no HSTS", "local", ""},
		{"unset env counts as local", "", ""},
		{"dev has HSTS", "dev", "max-age=31536000"},
		{"prod has HSTS", "prod", "max-age=31536000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("APP_ENV", tt.appEnv)
			h := httpx.SecurityHeaders()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) }))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/events", nil))

			assert.Equal(t, http.StatusTeapot, rec.Code, "the wrapped handler still runs")
			assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
			assert.Equal(t, "no-referrer", rec.Header().Get("Referrer-Policy"))
			assert.Equal(t, tt.wantHSTS, rec.Header().Get("Strict-Transport-Security"))
		})
	}
}

// The outermost placement means a CORS preflight (answered with 204 by CORS) carries the headers too.
func TestSecurityHeaders_OnPreflight(t *testing.T) {
	t.Setenv("APP_ENV", "prod")
	t.Setenv("CORS_ALLOWED_ORIGINS", "https://app.example.test")
	h := httpx.Chain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), httpx.SecurityHeaders(), httpx.CORS())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodOptions, "/api/v1/events", nil))

	require.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
}

// SEC-004: outside local the CORS allowlist must be set; the old wildcard is for unconfigured local dev only.
func TestCheckCORS(t *testing.T) {
	tests := []struct {
		name    string
		appEnv  string
		origins string
		wantErr bool
	}{
		{"local without list is allowed", "local", "", false},
		{"unset env without list is allowed", "", "", false},
		{"dev without list fails", "dev", "", true},
		{"prod without list fails", "prod", "", true},
		{"prod with blank entries only fails", "prod", " , ,", true},
		{"prod with list passes", "prod", "https://rusuhbooking.my.id", false},
		{"dev with list passes", "dev", "http://localhost:8081,https://x.example.test", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("APP_ENV", tt.appEnv)
			t.Setenv("CORS_ALLOWED_ORIGINS", tt.origins)
			err := httpx.CheckCORS()
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "CORS_ALLOWED_ORIGINS")
				return
			}
			require.NoError(t, err)
		})
	}
}
