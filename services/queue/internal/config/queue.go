package config

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/crypto"
)

// Token holds TOKEN_* variables. The signer mints admission/visitor/queue tokens (EdDSA);
// the verifier checks User Service bearer tokens with the shared public key.
type Token struct {
	Issuer         string
	Audience       string
	KeyID          string
	UserIssuer     string // Issuer of User Service tokens (for verifying buyer bearer tokens)
	UserKeyID      string // Key ID of User Service tokens (default: "user-1")
	PrivateKeyFile string
	PublicKeyFile  string // User Service public key for verifying buyer bearer tokens
	AdmissionTTL   time.Duration
	VisitorTTL     time.Duration
	QueueTTL       time.Duration
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
	if t.AdmissionTTL <= 0 || t.AdmissionTTL > 5*time.Minute {
		errs = append(errs, fmt.Errorf("TOKEN_ADMISSION_TTL %s di luar rentang (0, 5m]", t.AdmissionTTL))
	}
	if t.VisitorTTL <= 0 {
		errs = append(errs, errors.New("TOKEN_VISITOR_TTL harus > 0"))
	}
	if t.QueueTTL <= 0 {
		errs = append(errs, errors.New("TOKEN_QUEUE_TTL harus > 0"))
	}
	return errors.Join(errs...)
}

// PII holds the 32-byte hex keys for field protection. PII_HMAC_KEY MUST match User Service.
type PII struct {
	EncKeyHex  string // PII_ENC_KEY: AES-256-GCM for email_enc
	HashKeyHex string // PII_HMAC_KEY: HMAC for nik_hash/email_hash (must equal User Service key)
}

// Validate checks PII keys.
func (p PII) Validate() error {
	var errs []error
	for name, v := range map[string]string{"PII_ENC_KEY": p.EncKeyHex, "PII_HMAC_KEY": p.HashKeyHex} {
		if _, err := crypto.ParseKey(v); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
		}
	}
	if p.EncKeyHex != "" && strings.EqualFold(p.EncKeyHex, p.HashKeyHex) {
		errs = append(errs, errors.New("PII_ENC_KEY dan PII_HMAC_KEY harus berbeda"))
	}
	return errors.Join(errs...)
}

// Booking holds the outbound Booking Service client config.
type Booking struct {
	BaseURL string
	Timeout time.Duration
}

// Validate checks Booking.
func (b Booking) Validate() error {
	var errs []error
	if b.BaseURL == "" {
		errs = append(errs, errors.New("BOOKING_SERVICE_URL wajib diisi"))
	}
	if b.Timeout <= 0 {
		errs = append(errs, errors.New("BOOKING_TIMEOUT harus > 0"))
	}
	return errors.Join(errs...)
}

// RateLimit holds the per-endpoint fixed-window rate limits (design §2.1, §2.3, §2.4).
type RateLimit struct {
	JoinPerEmailWindow time.Duration
	JoinPerIPWindow    time.Duration
	MePerVisitorWindow time.Duration
	JoinPerEmail       int
	JoinPerIP          int
	MePerVisitor       int
}

// WorkerTuning holds fallback intervals for the background jobs (design §7).
type WorkerTuning struct {
	PromotorInterval time.Duration
	SweeperInterval  time.Duration
	StatusInterval   time.Duration
	PromoteBatchMax  int
}
