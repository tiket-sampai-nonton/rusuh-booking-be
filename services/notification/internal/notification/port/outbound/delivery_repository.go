package outbound

import (
	"context"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/domain/entity"
)

//go:generate go tool mockgen -source=delivery_repository.go -destination=../../../../_mock/notification/port/outbound/delivery_repository_mock.go -package=mock_outbound

// IDeliveryRepository persists delivery tracking rows. The ClaimDelivery insert with
// ON CONFLICT DO NOTHING is the idempotency guard (no separate inbox table, §9.8).
type IDeliveryRepository interface {
	// ClaimDelivery inserts a SENDING row. Returns true when claimed (first time) and false
	// when the (source_message_id, notification_type) pair already exists (duplicate → skip).
	ClaimDelivery(ctx context.Context, d entity.Delivery) (claimed bool, err error)
	// MarkSent transitions the row to SENT with sent_at and the provider message id.
	MarkSent(ctx context.Context, sourceMessageID string, t entity.NotificationType, providerMessageID string) error
	// MarkDead transitions the row to DEAD with the last error (permanent failure).
	MarkDead(ctx context.Context, sourceMessageID string, t entity.NotificationType, lastError string) error
	// MarkExpired transitions the row to DEAD and sets expires_at (late push discarded, not retried).
	MarkExpired(ctx context.Context, sourceMessageID string, t entity.NotificationType) error
	// BumpAttempt increments attempts, stores the last error, and sets the next attempt time (transient failure).
	BumpAttempt(ctx context.Context, sourceMessageID string, t entity.NotificationType, lastError string, nextAttemptAt time.Time) error
}
