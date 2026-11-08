package inbound

import (
	"errors"
	"net/http"
	"strings"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/apperror"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
	shared "github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/shared/entity"
)

// Queue-specific error codes (design §9). Shared codes (VALIDATION_ERROR, NOT_FOUND,
// RATE_LIMITED, DEPENDENCY_UNAVAILABLE) live in pkg/apperror and are reused.
const (
	CodeAccountInQueue      apperror.Code = "ACCOUNT_ALREADY_IN_QUEUE"
	CodeVisitorTokenInvalid apperror.Code = "VISITOR_TOKEN_INVALID"
	CodeEntryNotFound       apperror.Code = "ENTRY_NOT_FOUND"
	CodeEntryClaimed        apperror.Code = "ENTRY_CLAIMED"
	CodeEventNotOnSale      apperror.Code = "EVENT_NOT_ON_SALE"
	CodeEventSoldOut        apperror.Code = "EVENT_SOLD_OUT"
	CodeTurnMissed          apperror.Code = "TURN_MISSED"
	CodeNotAdmitted         apperror.Code = "NOT_ADMITTED"
	CodeEntryMoved          apperror.Code = "ENTRY_MOVED"
	CodeQueueNotConfigured  apperror.Code = "QUEUE_NOT_CONFIGURED"
	CodeEventRulesLocked    apperror.Code = "EVENT_RULES_LOCKED"
)

// ErrUnauthorized is returned by the admin guard for any authentication failure (401, never 403).
var ErrUnauthorized = apperror.New(apperror.CodeUnauthorized, http.StatusUnauthorized, "akses token tidak valid atau kedaluwarsa")

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
	{entity.ErrEntryClaimed, CodeEntryClaimed, http.StatusConflict},
	{entity.ErrNotOnSale, CodeEventNotOnSale, http.StatusConflict},
	{entity.ErrSoldOut, CodeEventSoldOut, http.StatusGone},
	{entity.ErrTurnMissed, CodeTurnMissed, http.StatusGone},
	{entity.ErrNotAdmitted, CodeNotAdmitted, http.StatusConflict},
	{entity.ErrEntryMoved, CodeEntryMoved, http.StatusConflict},
	{entity.ErrNotConfigured, CodeQueueNotConfigured, http.StatusConflict},
	{entity.ErrSettingsNotFound, CodeQueueNotConfigured, http.StatusNotFound},
	{entity.ErrRulesLocked, CodeEventRulesLocked, http.StatusConflict},
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
	var ve shared.ValidationErrors
	if errors.As(err, &ve) {
		// The client body only carries the message (details go to the log), so the fields are named here.
		return apperror.Wrap(err, apperror.CodeValidationFailed, http.StatusBadRequest, "permintaan tidak valid: "+validationSummary(ve)).
			WithDetails(shared.MapValidationErrors(ve))
	}
	for _, m := range errMappings {
		if errors.Is(err, m.target) {
			return apperror.Wrap(err, m.code, m.status, m.target.Error())
		}
	}
	return apperror.From(err)
}

// validationSummary lists "field: message" pairs in order; messages are static rule text, never request data.
func validationSummary(ve shared.ValidationErrors) string {
	parts := make([]string, 0, len(ve))
	for _, e := range ve {
		parts = append(parts, e.Field+": "+e.Message)
	}
	return strings.Join(parts, "; ")
}
