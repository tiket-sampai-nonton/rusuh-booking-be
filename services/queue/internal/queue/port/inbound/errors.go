package inbound

import (
	"errors"
	"net/http"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/apperror"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
)

// Queue-specific error codes (design §9). Shared codes (VALIDATION_ERROR, NOT_FOUND,
// RATE_LIMITED, DEPENDENCY_UNAVAILABLE) live in pkg/apperror and are reused.
const (
	CodeNikInvalid     apperror.Code = "NIK_INVALID"
	CodeNikRequired    apperror.Code = "NIK_REQUIRED"
	CodeEmailMismatch  apperror.Code = "EMAIL_MISMATCH"
	CodeNikInQueue     apperror.Code = "NIK_ALREADY_IN_QUEUE"
	CodeVisitorInQueue apperror.Code = "VISITOR_ALREADY_IN_QUEUE"
	CodeEmailInQueue   apperror.Code = "EMAIL_ALREADY_IN_QUEUE"
	CodeQuotaExhausted apperror.Code = "BUYER_QUOTA_EXHAUSTED"
	CodeEventNotOnSale apperror.Code = "EVENT_NOT_ON_SALE"
	CodeEventSoldOut   apperror.Code = "EVENT_SOLD_OUT"
	CodeTurnMissed     apperror.Code = "TURN_MISSED"
	CodeEntryMoved     apperror.Code = "ENTRY_MOVED"
)

// errMapping pairs a domain sentinel with its stable code and HTTP status.
type errMapping struct {
	target error
	code   apperror.Code
	status int
}

var errMappings = []errMapping{
	{entity.ErrNikInvalid, CodeNikInvalid, http.StatusUnprocessableEntity},
	{entity.ErrNikRequired, CodeNikRequired, http.StatusUnprocessableEntity},
	{entity.ErrEmailMismatch, CodeEmailMismatch, http.StatusUnprocessableEntity},
	{entity.ErrConsentNik, apperror.CodeValidationFailed, http.StatusBadRequest},
	{entity.ErrNikInQueue, CodeNikInQueue, http.StatusConflict},
	{entity.ErrVisitorInQueue, CodeVisitorInQueue, http.StatusConflict},
	{entity.ErrEmailInQueue, CodeEmailInQueue, http.StatusConflict},
	{entity.ErrQuotaExhausted, CodeQuotaExhausted, http.StatusConflict},
	{entity.ErrNotOnSale, CodeEventNotOnSale, http.StatusConflict},
	{entity.ErrSoldOut, CodeEventSoldOut, http.StatusGone},
	{entity.ErrTurnMissed, CodeTurnMissed, http.StatusGone},
	{entity.ErrEntryMoved, CodeEntryMoved, http.StatusConflict},
	{entity.ErrNotOwner, apperror.CodeNotFound, http.StatusNotFound},
	{entity.ErrNotFound, apperror.CodeNotFound, http.StatusNotFound},
	{entity.ErrEventNotFound, apperror.CodeNotFound, http.StatusNotFound},
	{entity.ErrDependencyUnavailable, apperror.CodeDependencyUnavailable, http.StatusServiceUnavailable},
}

// MapError converts a domain error into the apperror returned to clients. Unknown errors (and
// already-apperror values) are passed through to apperror.From, becoming a 500 unless typed.
func MapError(err error) *apperror.Error {
	if err == nil {
		return nil
	}
	var ae *apperror.Error
	if errors.As(err, &ae) {
		return ae
	}
	for _, m := range errMappings {
		if errors.Is(err, m.target) {
			return apperror.Wrap(err, m.code, m.status, m.target.Error())
		}
	}
	return apperror.From(err)
}
