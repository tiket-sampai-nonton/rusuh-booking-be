package config

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/crypto"
)

// Token holds TOKEN_* variables (access token EdDSA + refresh token acak).
type Token struct {
	Issuer         string
	Audience       string
	KeyID          string
	PrivateKeyFile string // PEM PKCS#8 Ed25519; public key-nya dibagikan ke Queue dan Booking
	AccessTTL      time.Duration
	RefreshTTL     time.Duration
	// MaxRefreshPerIPMinute membatasi refresh dan logout per IP per menit.
	MaxRefreshPerIPMinute int
}

// Validate checks Token.
func (t Token) Validate() error {
	var errs []error
	if t.Issuer == "" || t.Audience == "" || t.KeyID == "" {
		errs = append(errs, errors.New("TOKEN_ISSUER, TOKEN_AUDIENCE, dan TOKEN_KEY_ID wajib diisi"))
	}
	if t.PrivateKeyFile == "" {
		errs = append(errs, errors.New("TOKEN_PRIVATE_KEY_FILE wajib diisi"))
	}
	if t.AccessTTL < time.Minute || t.AccessTTL > time.Hour {
		errs = append(errs, fmt.Errorf("TOKEN_ACCESS_TTL %s di luar rentang 1m–1h", t.AccessTTL))
	}
	if t.RefreshTTL < time.Hour || t.RefreshTTL > 90*24*time.Hour {
		errs = append(errs, fmt.Errorf("TOKEN_REFRESH_TTL %s di luar rentang 1h–90d", t.RefreshTTL))
	}
	if t.MaxRefreshPerIPMinute < 1 || t.MaxRefreshPerIPMinute > 6000 {
		errs = append(errs, fmt.Errorf("TOKEN_MAX_REFRESH_PER_IP_MIN %d di luar rentang 1–6000", t.MaxRefreshPerIPMinute))
	}
	return errors.Join(errs...)
}

// OTP holds OTP_* variables.
type OTP struct {
	TTL             time.Duration
	ResendCooldown  time.Duration
	MaxAttempts     int
	MaxPerEmailHour int
	MaxPerIPHour    int
	// MaxVerifyPerIPMinute membatasi percobaan verifikasi OTP per IP per menit.
	MaxVerifyPerIPMinute int
}

// Validate checks OTP.
func (o OTP) Validate() error {
	var errs []error
	if o.TTL < time.Minute || o.TTL > 15*time.Minute {
		errs = append(errs, fmt.Errorf("OTP_TTL %s di luar rentang 1m–15m", o.TTL))
	}
	if o.ResendCooldown < 10*time.Second || o.ResendCooldown >= o.TTL {
		errs = append(errs, errors.New("OTP_RESEND_COOLDOWN harus ≥ 10s dan < OTP_TTL"))
	}
	if o.MaxAttempts < 3 || o.MaxAttempts > 10 {
		errs = append(errs, fmt.Errorf("OTP_MAX_ATTEMPTS %d di luar rentang 3–10", o.MaxAttempts))
	}
	if o.MaxPerEmailHour < 1 || o.MaxPerEmailHour > 10 {
		errs = append(errs, fmt.Errorf("OTP_MAX_PER_EMAIL_HOUR %d di luar rentang 1–10", o.MaxPerEmailHour))
	}
	if o.MaxPerIPHour < o.MaxPerEmailHour || o.MaxPerIPHour > 1000 {
		errs = append(errs, fmt.Errorf("OTP_MAX_PER_IP_HOUR %d harus ≥ OTP_MAX_PER_EMAIL_HOUR dan ≤ 1000", o.MaxPerIPHour))
	}
	// Anggaran tebakan per jam per email ≤ 100 dari 10^6 kemungkinan kode.
	if o.MaxAttempts*o.MaxPerEmailHour > 100 {
		errs = append(errs, fmt.Errorf("OTP_MAX_ATTEMPTS × OTP_MAX_PER_EMAIL_HOUR = %d melebihi batas 100", o.MaxAttempts*o.MaxPerEmailHour))
	}
	if o.MaxVerifyPerIPMinute < 1 || o.MaxVerifyPerIPMinute > 6000 {
		errs = append(errs, fmt.Errorf("OTP_MAX_VERIFY_PER_IP_MIN %d di luar rentang 1–6000", o.MaxVerifyPerIPMinute))
	}
	return errors.Join(errs...)
}

// PII holds the 32-byte hex keys for field protection.
type PII struct {
	EncKeyHex  string // AES-256-GCM untuk email_enc dan nik_enc
	HashKeyHex string // HMAC untuk email_hash dan nik_hash (harus sama dengan Queue/Booking agar nik_hash cocok)
	OTPKeyHex  string // HMAC untuk hash kode OTP di Valkey
}

// Validate checks PII keys.
func (p PII) Validate() error {
	var errs []error
	for name, v := range map[string]string{"PII_ENC_KEY": p.EncKeyHex, "PII_HASH_KEY": p.HashKeyHex, "OTP_HASH_KEY": p.OTPKeyHex} {
		if _, err := crypto.ParseKey(v); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
		}
	}
	// Ketiga pasangan dicek (tanpa syarat kunci terisi) dan pesan hanya menyebut nama env, tidak pernah isi kunci.
	for _, pair := range []struct{ an, bn, a, b string }{
		{"PII_ENC_KEY", "PII_HASH_KEY", p.EncKeyHex, p.HashKeyHex},
		{"PII_ENC_KEY", "OTP_HASH_KEY", p.EncKeyHex, p.OTPKeyHex},
		{"PII_HASH_KEY", "OTP_HASH_KEY", p.HashKeyHex, p.OTPKeyHex},
	} {
		if strings.EqualFold(pair.a, pair.b) {
			errs = append(errs, fmt.Errorf("%s dan %s harus berbeda", pair.an, pair.bn))
		}
	}
	return errors.Join(errs...)
}

// RabbitMQ holds RABBITMQ_* variables for publishing otp.requested events.
type RabbitMQ struct {
	URL         string
	Exchange    string
	ConfirmWait time.Duration
}

// Validate checks RabbitMQ config.
func (r RabbitMQ) Validate() error {
	var errs []error
	if r.URL == "" {
		errs = append(errs, errors.New("RABBITMQ_URL wajib diisi"))
	}
	if r.Exchange == "" {
		errs = append(errs, errors.New("RABBITMQ_EXCHANGE wajib diisi"))
	}
	if r.ConfirmWait <= 0 {
		errs = append(errs, errors.New("RABBITMQ_CONFIRM_TIMEOUT harus > 0"))
	}
	return errors.Join(errs...)
}
