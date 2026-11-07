package valueobject

import (
	"errors"
	"net/mail"
	"strings"
)

// ErrInvalidEmail is returned for an address that is not a plain RFC 5322 mailbox.
var ErrInvalidEmail = errors.New("valueobject: email tidak valid")

// ParseEmail trims and validates a plain address (no display name, no angle brackets), 3..254 chars.
func ParseEmail(raw string) (string, error) {
	e := strings.TrimSpace(raw)
	if len(e) < 3 || len(e) > 254 {
		return "", ErrInvalidEmail
	}
	a, err := mail.ParseAddress(e)
	if err != nil || a.Address != e || !strings.Contains(e, "@") {
		return "", ErrInvalidEmail
	}
	return e, nil
}

// EmailsMatch compares two addresses case-insensitively after trimming.
func EmailsMatch(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}
