package outbound

import (
	"context"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
)

//go:generate go tool mockgen -source=queue_store.go -destination=../../../../_mock/queue/port/outbound/queue_store_mock.go -package=mock_outbound

// IQueueStore is the Valkey-backed hot state of the queue. Lua scripts provide atomicity.
type IQueueStore interface {
	Join(ctx context.Context, p JoinParams) (JoinResult, error)
	Promote(ctx context.Context, eventID string, cands []PromoteCandidate, capacity int, nowMs int64) ([]string, error)
	Claim(ctx context.Context, eventID, entryID, visitorID string, nowMs int64) (ClaimResult, error)
	Leave(ctx context.Context, eventID, entryID, visitorID string, nowMs int64) (wasActive bool, err error)
	EndEntry(ctx context.Context, eventID, entryID, outcome string, nowMs int64) (final string, err error)
	GetEntry(ctx context.Context, eventID, entryID string) (entity.Entry, bool, error)
	FindByVisitor(ctx context.Context, eventID, visitorID string) (entity.Entry, bool, error)
	ActiveCount(ctx context.Context, eventID string) (int, error)
	TopWaiting(ctx context.Context, eventID string, n int) ([]string, error)
	ExpiredActive(ctx context.Context, eventID string, nowMs int64) ([]string, error)
	MarkMissed(ctx context.Context, eventID, entryID string, nowMs int64) (bool, error)
	// MarkMissedAndClean atomically sets MISSED + cleans NIK/visitor/email constraint keys (TC-QUE-09).
	MarkMissedAndClean(ctx context.Context, eventID, entryID string, nowMs int64) (bool, error)
	AddQuotaExhausted(ctx context.Context, eventID, nikHash string) error
	SetSale(ctx context.Context, eventID, saleState string, soldOut bool) error
	GetSale(ctx context.Context, eventID string) (SaleState, error)
	SetStatusDoc(ctx context.Context, eventID string, doc []byte, ttl time.Duration) error
	GetStatusDoc(ctx context.Context, eventID string) ([]byte, bool, error)
}
