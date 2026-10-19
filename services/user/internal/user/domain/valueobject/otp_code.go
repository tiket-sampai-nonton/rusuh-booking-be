package valueobject

import "errors"

// ErrInvalidOTPCode is returned when the code is not exactly 6 digits.
var ErrInvalidOTPCode = errors.New("kode OTP harus 6 digit")

// OTPCode is a 6-digit one-time code.
type OTPCode struct{ value string }

// NewOTPCode validates s.
func NewOTPCode(s string) (OTPCode, error) {
	if len(s) != 6 {
		return OTPCode{}, ErrInvalidOTPCode
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return OTPCode{}, ErrInvalidOTPCode
		}
	}
	return OTPCode{value: s}, nil
}

// String returns the digits.
func (c OTPCode) String() string { return c.value }
