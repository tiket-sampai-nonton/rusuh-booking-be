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
)

func TestMapError(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   apperror.Code
	}{
		{"nik invalid", entity.ErrNikInvalid, http.StatusUnprocessableEntity, inbound.CodeNikInvalid},
		{"nik required", entity.ErrNikRequired, http.StatusUnprocessableEntity, inbound.CodeNikRequired},
		{"email mismatch", entity.ErrEmailMismatch, http.StatusUnprocessableEntity, inbound.CodeEmailMismatch},
		{"consent", entity.ErrConsentNik, http.StatusBadRequest, apperror.CodeValidationFailed},
		{"nik in queue", entity.ErrNikInQueue, http.StatusConflict, inbound.CodeNikInQueue},
		{"visitor in queue", entity.ErrVisitorInQueue, http.StatusConflict, inbound.CodeVisitorInQueue},
		{"email in queue", entity.ErrEmailInQueue, http.StatusConflict, inbound.CodeEmailInQueue},
		{"quota", entity.ErrQuotaExhausted, http.StatusConflict, inbound.CodeQuotaExhausted},
		{"not on sale", entity.ErrNotOnSale, http.StatusConflict, inbound.CodeEventNotOnSale},
		{"sold out", entity.ErrSoldOut, http.StatusGone, inbound.CodeEventSoldOut},
		{"turn missed", entity.ErrTurnMissed, http.StatusGone, inbound.CodeTurnMissed},
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
