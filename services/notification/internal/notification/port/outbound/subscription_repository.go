package outbound

import (
	"context"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/domain/entity"
)

//go:generate go tool mockgen -source=subscription_repository.go -destination=../../../../_mock/notification/port/outbound/subscription_repository_mock.go -package=mock_outbound

// ISubscriptionRepository persists push subscriptions keyed by (visitor_id, event_id).
type ISubscriptionRepository interface {
	// Upsert stores or replaces a subscription (idempotent re-subscribe). expiresAt controls TTL.
	Upsert(ctx context.Context, sub entity.PushSubscription) error
	// Delete removes the subscription for (visitorID, eventID); no-op when absent (idempotent).
	Delete(ctx context.Context, visitorID, eventID string) error
	// FindByVisitorAndEvent returns the subscription or entity.ErrNotFound.
	FindByVisitorAndEvent(ctx context.Context, visitorID, eventID string) (entity.PushSubscription, error)
}
