package inbound

import (
	"context"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/domain/dto"
)

//go:generate go tool mockgen -source=subscription_usecase.go -destination=../../../../_mock/notification/port/inbound/subscription_usecase_mock.go -package=mock_inbound

// ISubscriptionUsecase manages device push subscriptions for the HTTP API.
type ISubscriptionUsecase interface {
	// Subscribe stores (or upserts) a push subscription for the given visitor.
	Subscribe(ctx context.Context, visitorID string, req dto.SubscribeRequest) error
	// Unsubscribe removes the visitor's subscription for eventID (idempotent).
	Unsubscribe(ctx context.Context, visitorID, eventID string) error
}
