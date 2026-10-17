package http

import (
	"context"
	"encoding/json"
	"errors"
	stdhttp "net/http"
	"net/http/httptest"
	"testing"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/apperror"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/dto"
)

type fakeReadiness struct {
	resp dto.ReadinessResponse
	err  error
}

func (f fakeReadiness) CheckReadiness(context.Context) (dto.ReadinessResponse, error) {
	return f.resp, f.err
}

func TestRoutes(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		uc       fakeReadiness
		wantCode int
	}{
		{"healthz", "/healthz", fakeReadiness{}, stdhttp.StatusOK},
		{"ready", "/readyz", fakeReadiness{resp: dto.ReadinessResponse{Status: dto.StatusReady}}, stdhttp.StatusOK},
		{"not ready", "/readyz", fakeReadiness{resp: dto.ReadinessResponse{Status: dto.StatusNotReady}}, stdhttp.StatusServiceUnavailable},
		{"usecase error", "/readyz", fakeReadiness{err: errors.New("boom")}, stdhttp.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()

			NewHandler("payment", tt.uc).Routes().ServeHTTP(rec, httptest.NewRequest(stdhttp.MethodGet, tt.path, nil))

			if rec.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tt.wantCode, rec.Body.String())
			}
		})
	}
}

func TestUsecaseErrorUsesEnvelope(t *testing.T) {
	rec := httptest.NewRecorder()

	NewHandler("payment", fakeReadiness{err: errors.New("boom")}).Routes().
		ServeHTTP(rec, httptest.NewRequest(stdhttp.MethodGet, "/readyz", nil))

	var env apperror.Envelope
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil || env.Error.Code != apperror.CodeInternal {
		t.Fatalf("envelope = %+v, err = %v", env, err)
	}
}
