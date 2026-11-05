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
	CodeAccountInQueue      apperror.Code = "ACCOUNT_ALREADY_IN_QUEUE"
	CodeVisitorTokenInvalid apperror.Code = "VISITOR_TOKEN_INVALID"
	CodeEntryNotFound       apperror.Code = "ENTRY_NOT_FOUND"
	CodeEventNotOnSale      apperror.Code = "EVENT_NOT_ON_SALE"
	CodeEventSoldOut        apperror.Code = "EVENT_SOLD_OUT"
	CodeTurnMissed          apperror.Code = "TURN_MISSED"
	CodeNotAdmitted         apperror.Code = "NOT_ADMITTED"
	CodeEntryMoved          apperror.Code = "ENTRY_MOVED"
)

// errMapping pairs a domain sentinel with its stable code and HTTP status.
type errMapping struct {
	target error
	code   apperror.Code
	status int
}

var errMappings = []errMapping{
	{entity.ErrAccountInQueue, CodeAccountInQueue, http.StatusConflict},
	{entity.ErrVisitorTokenInvalid, CodeVisitorTokenInvalid, http.StatusUnauthorized},
	{entity.ErrEntryNotFound, CodeEntryNotFound, http.StatusNotFound},
	{entity.ErrNotOnSale, CodeEventNotOnSale, http.StatusConflict},
	{entity.ErrSoldOut, CodeEventSoldOut, http.StatusGone},
	{entity.ErrTurnMissed, CodeTurnMissed, http.StatusGone},
	{entity.ErrNotAdmitted, CodeNotAdmitted, http.StatusConflict},
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
