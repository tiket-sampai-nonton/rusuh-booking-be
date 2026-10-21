package usecase

import (
	"context"
	"fmt"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/domain/mapper"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/port/outbound"
)

// ManageSubscription implements inbound.ISubscriptionUsecase: register/remove push subscriptions.
type ManageSubscription struct {
	subs  outbound.ISubscriptionRepository
	pii   outbound.IPIIProtector
	clock clock.IClock
	ttl   time.Duration
}

// NewManageSubscription accepts interfaces and returns the concrete usecase. ttl bounds expires_at.
func NewManageSubscription(subs outbound.ISubscriptionRepository, pii outbound.IPIIProtector, clk clock.IClock, ttl time.Duration) *ManageSubscription {
	return &ManageSubscription{subs: subs, pii: pii, clock: clk, ttl: ttl}
}

// Subscribe validates the request, encrypts the token, and upserts the subscription (idempotent).
func (u *ManageSubscription) Subscribe(ctx context.Context, visitorID string, req dto.SubscribeRequest) error {
	if details := req.Validate(); details != nil {
		return inbound.ValidationError(details)
	}
	tokenEnc, err := u.pii.SealPushToken(req.PushToken)
	if err != nil {
		return fmt.Errorf("seal push token: %w", err)
	}
	sub := mapper.ToSubscription(visitorID, req, tokenEnc)
	sub.CreatedAt = u.clock.Now()
	sub.ExpiresAt = u.clock.Now().Add(u.ttl)
	return u.subs.Upsert(ctx, sub)
}

// Unsubscribe removes the visitor's subscription for eventID (idempotent no-op when absent).
func (u *ManageSubscription) Unsubscribe(ctx context.Context, visitorID, eventID string) error {
	return u.subs.Delete(ctx, visitorID, eventID)
}
