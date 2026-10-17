// Package entity contains cross-domain reusable types for this service.
package entity

import "strings"

// ValidationError describes one invalid field.
type ValidationError struct {
	Field   string
	Message string
}

// ValidationErrors is a list of field errors and implements error.
type ValidationErrors []ValidationError

func (v ValidationErrors) Error() string {
	parts := make([]string, 0, len(v))
	for _, e := range v {
		parts = append(parts, e.Field+": "+e.Message)
	}
	return "validation failed: " + strings.Join(parts, "; ")
}

// MapValidationErrors converts field errors into a field→message map for the error envelope.
func MapValidationErrors(errs ValidationErrors) map[string]string {
	out := make(map[string]string, len(errs))
	for _, e := range errs {
		out[e.Field] = e.Message
	}
	return out
}
