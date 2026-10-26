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

// claimLease is how long a SENDING row stays owned by the worker that claimed it. A crash between
// claim and send leaves the row SENDING; once the lease lapses the broker redelivery may claim it again.
// shortcut: fixed value, not derived from the sender timeout (design §4.3: 2 × timeout); make it config when SMTP/Resend timeouts exceed 30 s.
const claimLease = 60 * time.Second

// ClaimDelivery claims one delivery for sending. It inserts a SENDING row, or takes over an existing
// row that is waiting for another attempt (PENDING/RETRY) or whose SENDING lease has lapsed. A row that
// is SENT, DEAD, or still held by another worker returns no row → claimed=false (duplicate, skip).
// The attempts counter is advanced by BumpAttempt on failure, not here.
func (r *Deliveries) ClaimDelivery(ctx context.Context, d entity.Delivery) (bool, error) {
	var id string
	err := r.pool.QueryRow(ctx, `
		INSERT INTO deliveries (source_message_id, notification_type, channel, booking_id, queue_entry_id,
		                        recipient_hash, status, attempts, next_attempt_at)
		VALUES ($1::uuid, $2, $3, $4::uuid, $5::uuid, $6, 'SENDING', 0, now() + make_interval(secs => $7))
		ON CONFLICT (source_message_id, notification_type) DO UPDATE
		   SET status = 'SENDING', next_attempt_at = now() + make_interval(secs => $7)
		 WHERE deliveries.status IN ('PENDING', 'RETRY')
		    OR (deliveries.status = 'SENDING' AND deliveries.next_attempt_at < now())
		RETURNING id::text`,
		d.SourceMessageID, string(d.Type), string(d.Channel), d.BookingID, d.QueueEntryID, d.RecipientHash,
		claimLease.Seconds(),
	).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil // SENT/DEAD/SENDING held by another worker → skip
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
