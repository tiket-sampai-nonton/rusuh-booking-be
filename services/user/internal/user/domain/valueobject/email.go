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
	// Satu aturan panjang, dalam byte: seluruh alamat ≤ 254 dan local part ≤ 64 (regex menghitung rune, jadi dicek eksplisit).
	if len(v) > 254 || !emailPattern.MatchString(v) {
		return Email{}, ErrInvalidEmail
	}
	local, _, _ := strings.Cut(v, "@")
	if len(local) > 64 || !isPrintableASCII(local) {
		return Email{}, ErrInvalidEmail
	}
	return Email{value: v}, nil
}

// String returns the normalized address.
func (e Email) String() string { return e.value }

// isPrintableASCII: setiap byte local part harus 0x21..0x7E (tanpa spasi, kontrol, atau UTF-8 multibyte).
// Plus-addressing (a+b@) tetap diterima karena '+' berada dalam rentang ini.
func isPrintableASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x21 || c > 0x7e {
			return false
		}
	}
	return true
}
