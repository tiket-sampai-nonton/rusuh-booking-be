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
	BaseURL string // BOOKING_SERVICE_URL: internal, service-to-service
	Timeout time.Duration
	// PageBaseURL is the public web URL of the booking page (BOOKING_PAGE_BASE_URL, e.g. https://app.example/book).
	// The claim response returns PageBaseURL + "/" + eventId; it never carries a token. Validated in Config.Validate
	// because the http-vs-https rule depends on APP_ENV.
	PageBaseURL string
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

	// StatusTTLMultiplier sets the published status-doc Valkey TTL as a multiple of the status publisher's own
	// cadence (QUEUE_STATUS_INTERVAL_MS), decoupling the key lifetime from the status publish interval so a direct
	// uncached GET /queue-status/{eventId} does not intermittently 404 between republishes
	// (QUEUE_STATUS_TTL_MULTIPLIER, bounds minStatusTTLMult..maxStatusTTLMult). Grounded in [[caching-strategy]].
	StatusTTLMultiplier int

	// PromoterLeaseTTL is the per-event promoter/admission lease TTL (QUEUE_PROMOTER_LEASE_TTL_MS).
	PromoterLeaseTTL time.Duration
	// SweepBatchMax bounds the expired entries the sweeper ends per event and tick (QUEUE_SWEEP_BATCH_MAX).
	SweepBatchMax int
	// NoSupplySuspectTicks labels a head that answers NO_SUPPLY_NOW this many consecutive ticks as suspect.
	NoSupplySuspectTicks int
}

// StatusTTL returns the Valkey TTL for the published status document: StatusTTLMultiplier times the
// effective publish cadence (interval). Validate keeps the multiplier in minStatusTTLMult..maxStatusTTLMult,
// so the TTL is always a few publish cycles long — never a single interval that would expire before the next
// republish (REQ-5). The status document stays a cache, never the source of truth (INV-4, [[caching-strategy]]).
func (w WorkerTuning) StatusTTL(interval time.Duration) time.Duration {
	return time.Duration(w.StatusTTLMultiplier) * interval
}

// Bounds of the new worker knobs.
const (
	minLeaseTTL        = 2 * time.Second
	maxLeaseTTL        = 30 * time.Second
	maxSweepBatch      = 5000
	minSuspectTicks    = 2
	minRateLimitWindow = time.Millisecond
	minStatusTTLMult   = 3
	maxStatusTTLMult   = 5
)

// Validate checks WorkerTuning against the booking client timeout (the lease must outlive two Booking calls).
func (w WorkerTuning) Validate(bookingTimeout time.Duration) error {
	var errs []error
	// Ordered slice (not a map) so the error order is deterministic.
	for _, iv := range []struct {
		name string
		d    time.Duration
	}{
		{"QUEUE_PROMOTOR_INTERVAL_MS", w.PromotorInterval},
		{"QUEUE_SWEEPER_INTERVAL_MS", w.SweeperInterval},
		{"QUEUE_STATUS_INTERVAL_MS", w.StatusInterval},
	} {
		if iv.d <= 0 {
			errs = append(errs, fmt.Errorf("%s %s harus > 0", iv.name, iv.d))
		}
	}
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
	if w.StatusTTLMultiplier < minStatusTTLMult || w.StatusTTLMultiplier > maxStatusTTLMult {
		errs = append(errs, fmt.Errorf("QUEUE_STATUS_TTL_MULTIPLIER %d di luar rentang %d..%d", w.StatusTTLMultiplier, minStatusTTLMult, maxStatusTTLMult))
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

// Admin holds the admin allow-list for the internal queue-settings endpoint (ADR-025). It is auth configuration, not
// a queue default: capacity and tuning are only ever set through the endpoint (QSA-8.1).
type Admin struct {
	// Emails are lower-cased, de-duplicated allow-list entries ("user@host" or "@domain"). Empty admits nobody.
	Emails []string
}

// parseAdminEmails splits a comma-separated list, trimming, lower-casing and de-duplicating while keeping order.
func parseAdminEmails(raw string) []string {
	var out []string
	seen := map[string]bool{}
	for _, e := range strings.Split(raw, ",") {
		e = strings.ToLower(strings.TrimSpace(e))
		if e == "" || seen[e] {
			continue
		}
		seen[e] = true
		out = append(out, e)
	}
	return out
}
