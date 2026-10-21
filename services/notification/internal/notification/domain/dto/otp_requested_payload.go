package dto

import (
	"encoding/base64"
	"errors"
	"fmt"
	"time"
)

// OTPRequestedPayload is the otp.requested.v1 payload (design §10.6). Email and code
// are AES-256-GCM ciphertext, base64url-encoded by the User Service publisher.
type OTPRequestedPayload struct {
	EmailEnc  string `json:"emailEnc"`
	CodeEnc   string `json:"codeEnc"`
	ExpiresAt string `json:"expiresAt"`
	Locale    string `json:"locale,omitempty"`
}

// Validate enforces the required fields of otp.requested.v1.
func (p OTPRequestedPayload) Validate() error {
	var errs []error
	if p.EmailEnc == "" {
		errs = append(errs, errors.New("emailEnc kosong"))
	}
	if p.CodeEnc == "" {
		errs = append(errs, errors.New("codeEnc kosong"))
	}
	if p.ExpiresAt == "" {
		errs = append(errs, errors.New("expiresAt kosong"))
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("otp.requested payload tidak valid: %w", err)
	}
	return nil
}

// DecodeEmailEnc returns the raw email ciphertext bytes.
func (p OTPRequestedPayload) DecodeEmailEnc() ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(p.EmailEnc)
}

// DecodeCodeEnc returns the raw code ciphertext bytes.
func (p OTPRequestedPayload) DecodeCodeEnc() ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(p.CodeEnc)
}

// ParseExpiresAt parses the RFC3339 expiry timestamp.
func (p OTPRequestedPayload) ParseExpiresAt() (time.Time, error) {
	return time.Parse(time.RFC3339, p.ExpiresAt)
}

// LocaleOrDefault returns the locale, defaulting to "id".
func (p OTPRequestedPayload) LocaleOrDefault() string {
	if p.Locale == "" {
		return "id"
	}
	return p.Locale
}
