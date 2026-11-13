package http_test

import (
	stdhttp "net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	mock_inbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/_mock/queue/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
	queuehttp "github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/infrastructure/http"
)

// P7 (REQ-QVT-03.7): the boundary between "generation record gone while the token is still valid" (404 NOT_FOUND) and
// "the token itself is past its lifetime" (rejected by the authenticator: 401 VISITOR_TOKEN_INVALID), through the real
// authenticator with a controlled clock. The "record gone" decision itself is the usecase's (TestTokenGenCheck_ThreeWay,
// real Valkey in visitor_token_integration_test.go); here the usecase is a stub answering that decision.
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
			uc := mock_inbound.NewMockIMyEntryUsecase(ctrl) // strict: reached only when the token is accepted
			if tt.storeHit {
				uc.EXPECT().GetMyEntry(gomock.Any(), gomock.Any()).Return(dto.EntryStatus{}, entity.ErrNotFound)
			}
			auth := g.authAt(g.now.Add(tt.at))
			mux := stdhttp.NewServeMux()
			queuehttp.NewMyEntryHandler(uc, auth).Register(mux)
			req := httptest.NewRequest(stdhttp.MethodGet, "/api/v1/queue-entries/me?eventId=5b1f2c9e-0000-4000-8000-000000000001", nil)
			req.Header.Set("X-Visitor-Token", g.unbound)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			require.Equal(t, tt.wantCode, rec.Code)
			assert.Contains(t, rec.Body.String(), tt.wantBody)
		})
	}
}
