// Package postgres implements the Notification repositories on schema notification (pgx).
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/domain/entity"
)

// Deliveries implements outbound.IDeliveryRepository.
type Deliveries struct{ pool *pgxpool.Pool }

// NewDeliveries returns the repository.
func NewDeliveries(pool *pgxpool.Pool) *Deliveries { return &Deliveries{pool: pool} }

// ClaimDelivery inserts a SENDING row. ON CONFLICT DO NOTHING is the idempotency guard: a conflict
// on (source_message_id, notification_type) returns no row → claimed=false (duplicate, skip).
func (r *Deliveries) ClaimDelivery(ctx context.Context, d entity.Delivery) (bool, error) {
	var id string
	err := r.pool.QueryRow(ctx, `
		INSERT INTO deliveries (source_message_id, notification_type, channel, booking_id, queue_entry_id,
		                        recipient_hash, status, attempts)
		VALUES ($1::uuid, $2, $3, $4::uuid, $5::uuid, $6, 'SENDING', 0)
		ON CONFLICT (source_message_id, notification_type) DO NOTHING
		RETURNING id::text`,
		d.SourceMessageID, string(d.Type), string(d.Channel), d.BookingID, d.QueueEntryID, d.RecipientHash,
	).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil // conflict → already processed
	}
	if err != nil {
		return false, fmt.Errorf("deliveries: claim: %w", err)
	}
	return true, nil
}

// MarkSent moves the row to SENT with sent_at and the provider message id.
func (r *Deliveries) MarkSent(ctx context.Context, sourceMessageID string, t entity.NotificationType, providerMessageID string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE deliveries SET status = 'SENT', sent_at = now(), provider_message_id = $3
		WHERE source_message_id = $1::uuid AND notification_type = $2`,
		sourceMessageID, string(t), providerMessageID)
	if err != nil {
		return fmt.Errorf("deliveries: mark sent: %w", err)
	}
	return nil
}

// MarkDead moves the row to DEAD with the last error (permanent failure).
func (r *Deliveries) MarkDead(ctx context.Context, sourceMessageID string, t entity.NotificationType, lastError string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE deliveries SET status = 'DEAD', last_error = $3
		WHERE source_message_id = $1::uuid AND notification_type = $2`,
		sourceMessageID, string(t), lastError)
	if err != nil {
		return fmt.Errorf("deliveries: mark dead: %w", err)
	}
	return nil
}

// MarkExpired moves the row to DEAD and records expires_at (late push discarded, not retried).
func (r *Deliveries) MarkExpired(ctx context.Context, sourceMessageID string, t entity.NotificationType) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE deliveries SET status = 'DEAD', expires_at = now()
		WHERE source_message_id = $1::uuid AND notification_type = $2`,
		sourceMessageID, string(t))
	if err != nil {
		return fmt.Errorf("deliveries: mark expired: %w", err)
	}
	return nil
}

// BumpAttempt increments attempts, stores the last error, sets status RETRY and next_attempt_at.
func (r *Deliveries) BumpAttempt(ctx context.Context, sourceMessageID string, t entity.NotificationType, lastError string, nextAttemptAt time.Time) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE deliveries SET status = 'RETRY', attempts = attempts + 1, last_error = $3, next_attempt_at = $4
		WHERE source_message_id = $1::uuid AND notification_type = $2`,
		sourceMessageID, string(t), lastError, nextAttemptAt)
	if err != nil {
		return fmt.Errorf("deliveries: bump attempt: %w", err)
	}
	return nil
}
