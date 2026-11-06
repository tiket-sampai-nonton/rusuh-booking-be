package outbound

import (
	"context"
	"time"
)

//go:generate go tool mockgen -source=services.go -destination=../../../../_mock/queue/port/outbound/services_mock.go -package=mock_outbound

// IBookingClient calls the Booking Service internal API.
type IBookingClient interface {
	ReserveBatch(ctx context.Context, req AllocationBatchRequest) (AllocationBatchResponse, error)
	Supply(ctx context.Context, eventID string) (SupplySnapshot, error)
	// ReleaseAllocation releases an admitted allocation as saga compensation when a buyer leaves
	// (spec 14.R1, ADR-018, req 7.2). Best-effort: Booking's own sweeper handles the fallback.
	ReleaseAllocation(ctx context.Context, allocationID string, reason string) error
}

// IEventPublisher publishes queue.turn.* events.
type IEventPublisher interface {
	PublishTurnSoon(ctx context.Context, eventID, queueEntryID, visitorID string) error
	PublishTurnArrived(ctx context.Context, eventID, queueEntryID, visitorID string, claimDeadline time.Time) error
	// PublishJourneyEnded tells Booking to release a reservation when a turn is missed (design §7.3).
	// NOTE: This is kept for now as Queue consumes booking.journey.ended; it is NOT called by the Sweeper (spec 11.R2).
	PublishJourneyEnded(ctx context.Context, eventID, queueEntryID, outcome string) error
}

// IRateLimiter is a fixed-window counter shared across instances (Valkey).
type IRateLimiter interface {
	Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, error)
}

// VisitorClaim is what a visitor token carries (ADR-030): the visitor id, the server-side token generation (a
// rotation revokes earlier generations) and an optional device hash (HMAC of X-Device-Id, never the raw value).
type VisitorClaim struct {
	VisitorID  string
	Gen        int64
	DeviceHash string
}

// ITokenIssuer signs admission/visitor/queue tokens (EdDSA).
type ITokenIssuer interface {
	IssueAdmission(entryID, eventID, allocationID string, now time.Time) (string, time.Time, error)
	IssueVisitor(c VisitorClaim, now time.Time) (string, error)
	IssueQueue(entryID, visitorID string, now time.Time) (string, error)
}

// IUserTokenVerifier verifies a User Service bearer token and extracts the buyer identity.
type IUserTokenVerifier interface {
	Verify(bearer string) (UserIdentity, error)
}

// IPromoterMetrics counts promoter and join events (labels as alternating key, value strings).
// Names and label sets are fixed by the telemetry adapter; an unknown name is dropped.
type IPromoterMetrics interface {
	Inc(name string, labels ...string)
}

// Counter names shared by the join usecase and the promoter (design §5.4a.2 rule 5). The telemetry adapter registers
// exactly these names; the port is their one home so application code never imports infrastructure.
const (
	MetricFullAfterGrant = "queue_promote_full_after_grant_total"
	MetricLeaseLost      = "queue_promote_lease_lost_total"
	MetricEntryEnded     = "queue_entry_ended_total" // label outcome
	MetricLapsedReplay   = "queue_promote_lapsed_replay_skipped_total"
	MetricFifoAdopted    = "queue_promote_fifo_adopted_total"          // label suspect
	MetricHeadNoSupply   = "queue_promote_head_no_supply_repeat_total" // label adopted
	MetricUnknownOutcome = "queue_promote_unknown_outcome_total"
	MetricJoinLeaseRetry = "queue_join_lease_retry_total"
)

// IAdminTokenVerifier authenticates an admin bearer (buyer scope + email allow-list, ADR-025) and returns the
// account id (token subject) used as the audit actor. Any failure returns an error; callers answer 401.
type IAdminTokenVerifier interface {
	VerifyAdmin(bearer string) (subject string, err error)
}
