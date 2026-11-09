package inbound

import (
	"context"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/dto"
)

//go:generate go tool mockgen -source=queue_usecase.go -destination=../../../../_mock/queue/port/inbound/queue_usecase_mock.go -package=mock_inbound

// JoinInput carries the request body plus the resolved request context (auth, visitor, client ip).
type JoinInput struct {
	EventID   string
	Body      dto.JoinRequest
	Bearer    string // User Service access token; "" for guest
	VisitorID string // existing visitor id (cookie/header); "" to mint a new one
	IsMobile  bool   // transport hint (X-Client-Type: mobile); the body-vs-cookie decision is made in the HTTP handler
	ClientIP  string
	// IdempotencyKey is the optional Idempotency-Key header (16..64 chars); a replay returns the same entry.
	IdempotencyKey string
	// TokenGen is the generation of the verified visitor token (0 = none); DeviceHash is the HMAC of X-Device-Id.
	TokenGen   int64
	DeviceHash string
}

// IJoinUsecase joins a queue (design §2.1).
type IJoinUsecase interface {
	Join(ctx context.Context, in JoinInput) (dto.QueueEntry, error)
}

// IStatusUsecase returns the public Status Document (design §2.2).
type IStatusUsecase interface {
	GetStatus(ctx context.Context, eventID string) (dto.StatusDocument, error)
}

// MyEntryInput identifies the visitor's entry for an event.
type MyEntryInput struct {
	EventID   string
	VisitorID string
	Gen       int64 // generation of the verified visitor token
}

// HeartbeatInput identifies the caller's entry for a liveness signal.
type HeartbeatInput struct {
	EventID   string
	VisitorID string
	Gen       int64
}

// IHeartbeatUsecase records a liveness signal (ADR-030, R03).
type IHeartbeatUsecase interface {
	Heartbeat(ctx context.Context, in HeartbeatInput) (dto.HeartbeatStatus, error)
}

// IMyEntryUsecase returns the caller's own entry status (design §2.3).
type IMyEntryUsecase interface {
	GetMyEntry(ctx context.Context, in MyEntryInput) (dto.EntryStatus, error)
}

// ClaimInput identifies the entry being claimed.
type ClaimInput struct {
	EntryID   string
	VisitorID string
	EventID   string
	Gen       int64 // generation of the verified visitor token; a rotated-out token is refused (ADR-030)
}

// IClaimUsecase claims an admitted turn (design §2.4).
type IClaimUsecase interface {
	Claim(ctx context.Context, in ClaimInput) (dto.ClaimResponse, error)
}

// LeaveInput identifies the entry being left.
type LeaveInput struct {
	EntryID   string
	VisitorID string
	EventID   string
	Gen       int64 // generation of the verified visitor token; a rotated-out token is refused (ADR-030)
}

// ILeaveUsecase removes the caller's entry (design §2.5).
type ILeaveUsecase interface {
	Leave(ctx context.Context, in LeaveInput) error
}
