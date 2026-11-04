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

	// PromoterLeaseTTL is the per-event promoter/admission lease TTL (QUEUE_PROMOTER_LEASE_TTL_MS).
	PromoterLeaseTTL time.Duration
	// SweepBatchMax bounds the expired entries the sweeper ends per event and tick (QUEUE_SWEEP_BATCH_MAX).
	SweepBatchMax int
	// UnsealFailMax is the number of failed ticks before an unreadable-PII head is ended (QUEUE_UNSEAL_FAIL_MAX).
	UnsealFailMax int
	// UnsealProbe is the number of extra ids probed to tell an entry-scoped from a systemic unseal failure.
	UnsealProbe int
	// NoSupplySuspectTicks labels a head that answers NO_SUPPLY_NOW this many consecutive ticks as suspect.
	NoSupplySuspectTicks int
}

// Bounds of the new worker knobs.
const (
	minLeaseTTL        = 2 * time.Second
	maxLeaseTTL        = 30 * time.Second
	maxSweepBatch      = 5000
	minSuspectTicks    = 2
	minRateLimitWindow = time.Millisecond
)

// Validate checks WorkerTuning against the booking client timeout (the lease must outlive two Booking calls).
func (w WorkerTuning) Validate(bookingTimeout time.Duration) error {
	var errs []error
	switch {
	case w.PromoterLeaseTTL < minLeaseTTL || w.PromoterLeaseTTL > maxLeaseTTL:
		errs = append(errs, fmt.Errorf("QUEUE_PROMOTER_LEASE_TTL_MS %s di luar rentang [%s, %s]", w.PromoterLeaseTTL, minLeaseTTL, maxLeaseTTL))
	case w.PromoterLeaseTTL <= 2*bookingTimeout:
		errs = append(errs, fmt.Errorf("QUEUE_PROMOTER_LEASE_TTL_MS %s harus > 2x BOOKING_TIMEOUT (%s)", w.PromoterLeaseTTL, 2*bookingTimeout))
	}
	if w.SweepBatchMax < 1 || w.SweepBatchMax > maxSweepBatch {
		errs = append(errs, fmt.Errorf("QUEUE_SWEEP_BATCH_MAX %d di luar rentang 1..%d", w.SweepBatchMax, maxSweepBatch))
	}
	if w.UnsealFailMax < 1 {
		errs = append(errs, fmt.Errorf("QUEUE_UNSEAL_FAIL_MAX %d harus >= 1", w.UnsealFailMax))
	}
	if w.UnsealProbe < 1 {
		errs = append(errs, fmt.Errorf("QUEUE_UNSEAL_PROBE %d harus >= 1", w.UnsealProbe))
	}
	if w.NoSupplySuspectTicks < minSuspectTicks {
		errs = append(errs, fmt.Errorf("QUEUE_NO_SUPPLY_SUSPECT_TICKS %d harus >= %d", w.NoSupplySuspectTicks, minSuspectTicks))
	}
	return errors.Join(errs...)
}

// Validate checks that every window is at least 1ms: a smaller window divides by zero in the limiter.
func (r RateLimit) Validate() error {
	var errs []error
	for name, w := range map[string]time.Duration{
		"RATE_LIMIT_JOIN_PER_EMAIL_WINDOW": r.JoinPerEmailWindow,
		"RATE_LIMIT_JOIN_PER_IP_WINDOW":    r.JoinPerIPWindow,
		"RATE_LIMIT_ME_PER_VISITOR_WINDOW": r.MePerVisitorWindow,
	} {
		if w < minRateLimitWindow {
			errs = append(errs, fmt.Errorf("%s %s harus >= %s", name, w, minRateLimitWindow))
		}
	}
	return errors.Join(errs...)
}
