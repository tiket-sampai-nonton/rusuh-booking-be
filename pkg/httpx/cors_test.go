package httpx_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/httpx"
)

// TC-QUE-44 (REQ-QVT-04.1/1a): the local CORS preflight allows X-Device-Id and still allows every header that was
// allowed before; Origin, Methods, Expose and the 204 are unchanged. A browser client that sends X-Device-Id
// (visitor-token device binding) would otherwise be blocked by the preflight.
func TestCORS_PreflightAllowHeaders(t *testing.T) {
	wantHeaders := []string{
		"Content-Type", "Authorization", "X-Visitor-Token", "X-Visitor-ID", "X-Queue-Token",
		"X-Client-Type", "Idempotency-Key", "X-Request-ID",
	}
	tests := []struct {
		name   string
		method string
		path   string
	}{
		{"preflight on a queue route", http.MethodOptions, "/api/v1/events/ev1/queue-entries"},
		{"preflight on another route", http.MethodOptions, "/api/v1/queue-entries/me"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			h := httpx.CORS()(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
			req := httptest.NewRequest(tt.method, tt.path, nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			require.Equal(t, http.StatusNoContent, rec.Code)
			assert.False(t, called, "a preflight never reaches the handler")
			allow := map[string]bool{}
			for _, v := range strings.Split(rec.Header().Get("Access-Control-Allow-Headers"), ",") {
				allow[strings.TrimSpace(v)] = true
			}
			for _, w := range wantHeaders {
				assert.True(t, allow[w], "Allow-Headers must contain %s", w)
			}
			assert.Equal(t, "*", rec.Header().Get("Access-Control-Allow-Origin"))
			assert.Equal(t, "GET, POST, PUT, PATCH, DELETE, OPTIONS", rec.Header().Get("Access-Control-Allow-Methods"))
			assert.Equal(t, "X-Request-ID, Set-Cookie", rec.Header().Get("Access-Control-Expose-Headers"))
		})
	}
}
