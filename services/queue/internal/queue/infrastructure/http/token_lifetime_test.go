package http_test

import (
	stdhttp "net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	mock_outbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/_mock/queue/port/outbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/application/usecase"
	queuehttp "github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/infrastructure/http"
)

// P7 (REQ-QVT-03.7): the boundary between "generation record gone while the token is still valid" (404 NOT_FOUND) and
// "the token itself is past its lifetime" (rejected by the authenticator: 401 VISITOR_TOKEN_INVALID), through the real
// authenticator and the real `me` usecase with a controlled clock.
func TestTokenLifetimeBoundary_RecordGoneVersusTokenExpired(t *testing.T) {
	g := newDeviceRig(t) // token issued at g.now with a 24h lifetime, no device claim
	tests := []struct {
		name     string
		at       time.Duration // clock offset from issuance
		wantCode int
		wantBody string
		storeHit bool
	}{
		{"inside lifetime, record removed -> 404", 23 * time.Hour, stdhttp.StatusNotFound, "NOT_FOUND", true},
		{"past lifetime -> 401", 24*time.Hour + 5*time.Second, stdhttp.StatusUnauthorized, "VISITOR_TOKEN_INVALID", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			store := mock_outbound.NewMockIQueueStore(ctrl) // strict: only TokenGen may be called, and only when the token is accepted
			if tt.storeHit {
				store.EXPECT().TokenGen(gomock.Any(), "ev1", "v1").Return(int64(0), false, nil)
			}
			auth := g.authAt(g.now.Add(tt.at))
			mux := stdhttp.NewServeMux()
			queuehttp.NewMyEntryHandler(usecase.NewGetMyEntry(store), auth).Register(mux)
			req := httptest.NewRequest(stdhttp.MethodGet, "/api/v1/queue-entries/me?eventId=ev1", nil)
			req.Header.Set("X-Visitor-Token", g.unbound)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			require.Equal(t, tt.wantCode, rec.Code)
			assert.Contains(t, rec.Body.String(), tt.wantBody)
		})
	}
}
