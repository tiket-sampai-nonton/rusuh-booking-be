package config

import (
	"errors"
	"fmt"
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

// PII holds the HMAC key used for the visitor-token device claim (ADR-030). Queue no longer receives or stores NIK or
// email, so it has no encryption key (PII_ENC_KEY is not read; leaving it set in the environment is harmless).
type PII struct {
	HashKeyHex string // PII_HMAC_KEY: HMAC-SHA256 of X-Device-Id (the raw value is never stored)
}

// Validate checks the PII key.
func (p PII) Validate() error {
	if _, err := crypto.ParseKey(p.HashKeyHex); err != nil {
		return fmt.Errorf("PII_HMAC_KEY: %w", err)
	}
	return nil
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
	JoinPerIPWindow     time.Duration
	MePerVisitorWindow  time.Duration
	JoinPerIP           int
	MePerVisitor        int
	HeartbeatPerVisitor int // per MePerVisitorWindow
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
	if w.NoSupplySuspectTicks < minSuspectTicks {
		errs = append(errs, fmt.Errorf("QUEUE_NO_SUPPLY_SUSPECT_TICKS %d harus >= %d", w.NoSupplySuspectTicks, minSuspectTicks))
	}
	return errors.Join(errs...)
}

// Validate checks that every window is at least 1ms: a smaller window divides by zero in the limiter.
func (r RateLimit) Validate() error {
	var errs []error
	for name, w := range map[string]time.Duration{
		"RATE_LIMIT_JOIN_PER_IP_WINDOW":    r.JoinPerIPWindow,
		"RATE_LIMIT_ME_PER_VISITOR_WINDOW": r.MePerVisitorWindow,
	} {
		if w < minRateLimitWindow {
			errs = append(errs, fmt.Errorf("%s %s harus >= %s", name, w, minRateLimitWindow))
		}
	}
	return errors.Join(errs...)
}

// Liveness holds the heartbeat/STALE knobs (ADR-030, R03.8) and the liveness-index shard count.
type Liveness struct {
	HeartbeatInterval time.Duration // QUEUE_HEARTBEAT_INTERVAL_SECONDS (±20% jitter)
	NearInterval      time.Duration // QUEUE_HEARTBEAT_NEAR_INTERVAL_SECONDS, used near the head
	StaleAfter        time.Duration // QUEUE_STALE_AFTER_SECONDS: silent longer than this -> STALE
	EndAfter          time.Duration // QUEUE_END_AFTER_SECONDS: silent longer than this -> ENDED (only without admission)
	BatchMax          int           // QUEUE_LIVENESS_BATCH_MAX entries marked/ended per event and shard per tick
	Shards            int           // QUEUE_LIVENESS_SHARDS: hb/hbstale index is split across this many keys (ADR-030 Amendment)
}

// Validate checks end > stale > 2·interval and the ranges.
func (l Liveness) Validate() error {
	var errs []error
	if l.HeartbeatInterval < time.Second {
		errs = append(errs, fmt.Errorf("QUEUE_HEARTBEAT_INTERVAL_SECONDS %s harus >= 1s", l.HeartbeatInterval))
	}
	if l.NearInterval < time.Second || l.NearInterval > l.HeartbeatInterval {
		errs = append(errs, fmt.Errorf("QUEUE_HEARTBEAT_NEAR_INTERVAL_SECONDS %s harus di [1s, interval]", l.NearInterval))
	}
	if l.StaleAfter <= 2*l.HeartbeatInterval {
		errs = append(errs, fmt.Errorf("QUEUE_STALE_AFTER_SECONDS %s harus > 2x interval heartbeat (%s)", l.StaleAfter, 2*l.HeartbeatInterval))
	}
	if l.EndAfter <= l.StaleAfter {
		errs = append(errs, fmt.Errorf("QUEUE_END_AFTER_SECONDS %s harus > QUEUE_STALE_AFTER_SECONDS (%s)", l.EndAfter, l.StaleAfter))
	}
	if l.BatchMax < 1 || l.BatchMax > 10000 {
		errs = append(errs, fmt.Errorf("QUEUE_LIVENESS_BATCH_MAX %d di luar rentang 1..10000", l.BatchMax))
	}
	if l.Shards < 1 || l.Shards > 256 {
		errs = append(errs, fmt.Errorf("QUEUE_LIVENESS_SHARDS %d di luar rentang 1..256", l.Shards))
	}
	return errors.Join(errs...)
}
