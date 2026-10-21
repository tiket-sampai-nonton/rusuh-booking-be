package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	pkgpg "github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/postgres"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/domain/entity"
)

// Subscriptions implements outbound.ISubscriptionRepository.
type Subscriptions struct{ pool *pgxpool.Pool }

// NewSubscriptions returns the repository.
func NewSubscriptions(pool *pgxpool.Pool) *Subscriptions { return &Subscriptions{pool: pool} }

// Upsert stores or replaces the subscription for (visitor_id, event_id) — idempotent re-subscribe.
func (r *Subscriptions) Upsert(ctx context.Context, sub entity.PushSubscription) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO push_subscriptions (visitor_id, event_id, platform, push_token_enc, created_at, expires_at)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6)
		ON CONFLICT (visitor_id, event_id) DO UPDATE
		SET platform = EXCLUDED.platform, push_token_enc = EXCLUDED.push_token_enc, expires_at = EXCLUDED.expires_at`,
		sub.VisitorID, sub.EventID, string(sub.Platform), sub.PushTokenEnc, sub.CreatedAt, sub.ExpiresAt)
	if err != nil {
		return fmt.Errorf("subscriptions: upsert: %w", err)
	}
	return nil
}

// Delete removes the subscription (no-op when absent; idempotent).
func (r *Subscriptions) Delete(ctx context.Context, visitorID, eventID string) error {
	_, err := r.pool.Exec(ctx, `
		DELETE FROM push_subscriptions WHERE visitor_id = $1::uuid AND event_id = $2::uuid`,
		visitorID, eventID)
	if err != nil {
		return fmt.Errorf("subscriptions: delete: %w", err)
	}
	return nil
}

// FindByVisitorAndEvent returns the subscription or entity.ErrNotFound.
func (r *Subscriptions) FindByVisitorAndEvent(ctx context.Context, visitorID, eventID string) (entity.PushSubscription, error) {
	var sub entity.PushSubscription
	var platform string
	err := r.pool.QueryRow(ctx, `
		SELECT visitor_id::text, event_id::text, platform, push_token_enc, created_at, expires_at
		FROM push_subscriptions WHERE visitor_id = $1::uuid AND event_id = $2::uuid`,
		visitorID, eventID).Scan(&sub.VisitorID, &sub.EventID, &platform, &sub.PushTokenEnc, &sub.CreatedAt, &sub.ExpiresAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows), pkgpg.IsCode(err, "22P02"):
		return entity.PushSubscription{}, entity.ErrNotFound
	case err != nil:
		return entity.PushSubscription{}, fmt.Errorf("subscriptions: find: %w", err)
	}
	sub.Platform = entity.Platform(platform)
	return sub, nil
}
