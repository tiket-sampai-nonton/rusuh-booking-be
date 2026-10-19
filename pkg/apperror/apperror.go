// Package apperror defines application error types and the stable JSON error envelope.
package apperror

import (
	"errors"
	"fmt"
	"net/http"
)

// Code is a stable, machine-readable error code returned to clients.
type Code string

// Error codes shared by all services. Service-specific codes are added next to the domain errors
// and mapped in infrastructure/http.
const (
	CodeInternal              Code = "INTERNAL_ERROR"
	CodeValidationFailed      Code = "VALIDATION_ERROR"
	CodeNotFound              Code = "NOT_FOUND"
	CodeDependencyUnavailable Code = "DEPENDENCY_UNAVAILABLE"
	CodeRateLimited           Code = "RATE_LIMITED"
)

// Error is an application error carrying an HTTP status and a stable code.
type Error struct {
	Code       Code
	Message    string
	HTTPStatus int
	Details    map[string]string
	cause      error
}

// New creates an application error.
func New(code Code, httpStatus int, message string) *Error {
	return &Error{Code: code, HTTPStatus: httpStatus, Message: message}
}

// Wrap creates an application error that keeps the underlying cause for logging.
func Wrap(cause error, code Code, httpStatus int, message string) *Error {
	return &Error{Code: code, HTTPStatus: httpStatus, Message: message, cause: cause}
}

// WithDetails returns a copy of the error with field-level details.
func (e *Error) WithDetails(details map[string]string) *Error {
	c := *e
	c.Details = details
	return &c
}

func (e *Error) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.cause)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Unwrap returns the underlying cause.
func (e *Error) Unwrap() error { return e.cause }

// Internal wraps an unexpected error as a 500 without leaking its message.
func Internal(cause error) *Error {
	return Wrap(cause, CodeInternal, http.StatusInternalServerError, "terjadi kesalahan internal")
}

// From converts any error into an application error. Unknown errors become INTERNAL_ERROR.
func From(err error) *Error {
	var ae *Error
	if errors.As(err, &ae) {
		return ae
	}
	return Internal(err)
}

// Envelope is the JSON error body returned by every service (SRS §3.1.4 Error Envelope):
//
//	{"meta":{"code":409,"message":"…","requestId":"…","errorCode":"…","details":{…}},"data":null}
type Envelope struct {
	Meta Meta `json:"meta"`
	Data any  `json:"data"`
}

// Meta is the meta part of every response envelope.
type Meta struct {
	Code      int               `json:"code"`
	Message   string            `json:"message,omitempty"`
	RequestID string            `json:"requestId,omitempty"`
	ErrorCode Code              `json:"errorCode,omitempty"`
	Details   map[string]string `json:"details,omitempty"`
}

// ToEnvelope builds the JSON envelope for the error.
func (e *Error) ToEnvelope(requestID string) Envelope {
	return Envelope{Meta: Meta{Code: e.HTTPStatus, Message: e.Message, RequestID: requestID, ErrorCode: e.Code, Details: e.Details}}
}
