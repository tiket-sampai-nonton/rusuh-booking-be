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

// ITokenIssuer signs admission/visitor/queue tokens (EdDSA).
type ITokenIssuer interface {
	IssueAdmission(entryID, eventID, allocationID string, now time.Time) (string, time.Time, error)
	IssueVisitor(visitorID string, now time.Time) (string, error)
	IssueQueue(entryID, visitorID string, now time.Time) (string, error)
}

// IUserTokenVerifier verifies a User Service bearer token and extracts the buyer identity.
type IUserTokenVerifier interface {
	Verify(bearer string) (UserIdentity, error)
}

// IPIIProtector hashes NIK/email and seals/unseals email.
type IPIIProtector interface {
	HashNIK(nik string) []byte
	HashEmail(email string) []byte
	SealEmail(email string) ([]byte, error)
	// SealBuyerEmail returns the base64 ciphertext sent to Booking (never the plaintext).
	SealBuyerEmail(email string) (string, error)
	UnsealEmail(enc []byte) (string, error)
}
