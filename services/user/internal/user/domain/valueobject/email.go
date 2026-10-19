package valueobject

import (
	"errors"
	"regexp"
	"strings"
)

// ErrInvalidEmail is returned for malformed email addresses.
var ErrInvalidEmail = errors.New("format email tidak valid")

var emailPattern = regexp.MustCompile(`^[^@\s]{1,64}@[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?(?:\.[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?)+$`)

// Email is a normalized (trimmed, lowercase) email address.
type Email struct{ value string }

// NewEmail validates and normalizes s.
func NewEmail(s string) (Email, error) {
	v := strings.ToLower(strings.TrimSpace(s))
	if len(v) > 254 || !emailPattern.MatchString(v) {
		return Email{}, ErrInvalidEmail
	}
	return Email{value: v}, nil
}

// String returns the normalized address.
func (e Email) String() string { return e.value }
