package valueobject

import (
	"encoding/hex"
	"errors"
	"regexp"
)

// ErrNikInvalid is returned when a NIK is not exactly 16 digits.
var ErrNikInvalid = errors.New("queue: NIK harus 16 digit angka")

// BuyerNikHash is the HMAC-SHA256 of a NIK (binary form).
type BuyerNikHash []byte

// Hex returns the lowercase hex encoding.
func (b BuyerNikHash) Hex() string { return hex.EncodeToString(b) }

var nikPattern = regexp.MustCompile(`^[0-9]{16}$`)

// ValidateNIKFormat checks that nik is exactly 16 digits. Returns ErrNikInvalid otherwise.
func ValidateNIKFormat(nik string) error {
	if !nikPattern.MatchString(nik) {
		return ErrNikInvalid
	}
	return nil
}
