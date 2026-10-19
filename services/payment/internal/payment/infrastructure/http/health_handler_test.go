package http

import (
	"encoding/json"
	stdhttp "net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	mock_inbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/_mock/payment/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/dto"
)

func TestHealthHandler(t *testing.T) {
	tests := []struct {
		name       string
		resp       dto.HealthResponse
		wantStatus int
	}{
		{"up", dto.HealthResponse{Status: dto.StatusUp, Service: "payment"}, stdhttp.StatusOK},
		{"down", dto.HealthResponse{Status: dto.StatusDown, Service: "payment"}, stdhttp.StatusServiceUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			uc := mock_inbound.NewMockIHealthUsecase(ctrl)
			uc.EXPECT().CheckHealth(gomock.Any()).Return(tt.resp)

			mux := stdhttp.NewServeMux()
			NewHealthHandler(uc).Register(mux)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(stdhttp.MethodGet, "/health", nil))

			assert.Equal(t, tt.wantStatus, rec.Code)
			assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
			var body dto.HealthResponse
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
			assert.Equal(t, tt.resp.Status, body.Status)
		})
	}
}

func TestHealthHandler_MethodNotAllowed(t *testing.T) {
	ctrl := gomock.NewController(t)
	mux := stdhttp.NewServeMux()
	NewHealthHandler(mock_inbound.NewMockIHealthUsecase(ctrl)).Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(stdhttp.MethodPost, "/health", nil))
	assert.Equal(t, stdhttp.StatusMethodNotAllowed, rec.Code)
}
