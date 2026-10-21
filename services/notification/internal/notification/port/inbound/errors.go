package inbound

import (
	"net/http"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/apperror"
)

// Service-specific error codes (mapped to the HTTP error envelope).
const (
	CodePushTokenInvalid apperror.Code = "PUSH_TOKEN_INVALID"
)

// ErrUnauthorized signals a missing or invalid visitor token (401).
var ErrUnauthorized = apperror.New(apperror.CodeValidationFailed, http.StatusUnauthorized, "token pengunjung tidak valid")

// ValidationError builds a 400 VALIDATION_ERROR carrying field-level details.
func ValidationError(details map[string]string) *apperror.Error {
	return apperror.New(apperror.CodeValidationFailed, http.StatusBadRequest, "payload tidak valid").WithDetails(details)
}
