package inbound_test

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/apperror"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/inbound"
	shared "github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/shared/entity"
)

func TestMapError(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   apperror.Code
	}{
		{"not on sale", entity.ErrNotOnSale, http.StatusConflict, inbound.CodeEventNotOnSale},
		{"sold out", entity.ErrSoldOut, http.StatusGone, inbound.CodeEventSoldOut},
		{"turn missed", entity.ErrTurnMissed, http.StatusGone, inbound.CodeTurnMissed},
		{"not admitted", entity.ErrNotAdmitted, http.StatusConflict, inbound.CodeNotAdmitted},
		{"not found", entity.ErrNotFound, http.StatusNotFound, apperror.CodeNotFound},
		{"not owner maps to not found", entity.ErrNotOwner, http.StatusNotFound, apperror.CodeNotFound},
		{"dependency", entity.ErrDependencyUnavailable, http.StatusServiceUnavailable, apperror.CodeDependencyUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ae := inbound.MapError(tt.err)
			assert.Equal(t, tt.wantStatus, ae.HTTPStatus)
			assert.Equal(t, tt.wantCode, ae.Code)
		})
	}
}

func TestMapError_WrappedSentinel(t *testing.T) {
	ae := inbound.MapError(fmt.Errorf("context: %w", entity.ErrTurnMissed))
	assert.Equal(t, http.StatusGone, ae.HTTPStatus)
	assert.Equal(t, inbound.CodeTurnMissed, ae.Code)
}

func TestMapError_PassthroughAppError(t *testing.T) {
	src := apperror.New(apperror.CodeRateLimited, http.StatusTooManyRequests, "rl")
	ae := inbound.MapError(src)
	assert.Same(t, src, ae)
}

func TestMapError_UnknownBecomesInternal(t *testing.T) {
	ae := inbound.MapError(errors.New("boom"))
	assert.Equal(t, http.StatusInternalServerError, ae.HTTPStatus)
	assert.Equal(t, apperror.CodeInternal, ae.Code)
}

func TestMapError_Nil(t *testing.T) {
	assert.Nil(t, inbound.MapError(nil))
}

func TestMapError_QueueSettings(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   apperror.Code
	}{
		{"not configured join", entity.ErrNotConfigured, http.StatusConflict, inbound.CodeQueueNotConfigured},
		{"settings not found get", entity.ErrSettingsNotFound, http.StatusNotFound, inbound.CodeQueueNotConfigured},
		{"rules locked", entity.ErrRulesLocked, http.StatusConflict, inbound.CodeEventRulesLocked},
		{"event not found", entity.ErrEventNotFound, http.StatusNotFound, apperror.CodeNotFound},
		{"wrapped rules locked", fmt.Errorf("apply: %w", entity.ErrRulesLocked), http.StatusConflict, inbound.CodeEventRulesLocked},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ae := inbound.MapError(tt.err)
			assert.Equal(t, tt.wantStatus, ae.HTTPStatus)
			assert.Equal(t, tt.wantCode, ae.Code)
		})
	}
}

func TestMapError_ValidationErrorsCarryDetails(t *testing.T) {
	ae := inbound.MapError(shared.ValidationErrors{{Field: "pollNearMs", Message: "terlalu besar"}})
	assert.Equal(t, http.StatusBadRequest, ae.HTTPStatus)
	assert.Equal(t, apperror.CodeValidationFailed, ae.Code)
	assert.Equal(t, map[string]string{"pollNearMs": "terlalu besar"}, ae.Details)
	wrapped := inbound.MapError(fmt.Errorf("x: %w", shared.ValidationErrors{{Field: "a", Message: "b"}}))
	assert.Equal(t, apperror.CodeValidationFailed, wrapped.Code)
}

func TestErrUnauthorized(t *testing.T) {
	assert.Equal(t, http.StatusUnauthorized, inbound.ErrUnauthorized.HTTPStatus)
	assert.Equal(t, apperror.CodeUnauthorized, inbound.ErrUnauthorized.Code)
	assert.Same(t, inbound.ErrUnauthorized, inbound.MapError(inbound.ErrUnauthorized))
}
