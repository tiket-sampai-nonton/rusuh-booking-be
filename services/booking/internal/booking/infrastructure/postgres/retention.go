package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

// Retention deletes rows past their retention window (retention job, D-C7.8). Every statement deletes at most limit rows
// chosen with FOR UPDATE SKIP LOCKED, so a row another session holds is skipped and the job never waits on a lock.
type Retention struct{ pool *pgxpool.Pool }

var _ outbound.IRetention = (*Retention)(nil)

// NewRetention returns the retention adapter.
func NewRetention(pool *pgxpool.Pool) *Retention { return &Retention{pool: pool} }

// PurgePublishedOutbox deletes published outbox rows older than before (idx_booking_outbox_published). The predicate on
// published_at keeps unpublished rows out of reach.
func (r *Retention) PurgePublishedOutbox(ctx context.Context, before time.Time, limit int) (int, error) {
	tag, err := r.pool.Exec(ctx, `
		DELETE FROM booking.outbox_events
		 WHERE ctid IN (SELECT ctid FROM booking.outbox_events
		                 WHERE published_at IS NOT NULL AND published_at < $1
		                 LIMIT $2 FOR UPDATE SKIP LOCKED)`, before, limit)
	if err != nil {
		return 0, fmt.Errorf("retention: outbox: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// PurgeExpiredIdempotency deletes idempotency records past expires_at (idx_booking_idem_expiry). The predicate is re-checked
// after the row lock, so a record that Begin takes over in the meantime is not deleted. An IN_PROGRESS record created
// within the 60 s lease (B9) is kept even when expired: its request may still be running.
func (r *Retention) PurgeExpiredIdempotency(ctx context.Context, now time.Time, limit int) (int, error) {
	tag, err := r.pool.Exec(ctx, `
		DELETE FROM booking.idempotency_records
		 WHERE ctid IN (SELECT ctid FROM booking.idempotency_records
		                 WHERE expires_at < $1
		                   AND NOT (state = 'IN_PROGRESS' AND created_at > $1 - interval '60 seconds')
		                 LIMIT $2 FOR UPDATE SKIP LOCKED)`, now, limit)
	if err != nil {
		return 0, fmt.Errorf("retention: idempotency: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// PurgeAttendeeChecks deletes the attendee checks of admissions that ended before endedBefore (idx_admissions_ended). Only
// terminal admissions have ended_at, so checks of an active or editable admission are never selected. The admissions rows
// are not locked: FOR UPDATE OF locks only the attendee_checks rows.
func (r *Retention) PurgeAttendeeChecks(ctx context.Context, endedBefore time.Time, limit int) (int, error) {
	tag, err := r.pool.Exec(ctx, `
		DELETE FROM booking.attendee_checks
		 WHERE ctid IN (SELECT ac.ctid
		                  FROM booking.attendee_checks ac
		                  JOIN booking.admissions a ON a.id = ac.admission_id
		                 WHERE a.ended_at IS NOT NULL AND a.ended_at < $1
		                 LIMIT $2 FOR UPDATE OF ac SKIP LOCKED)`, endedBefore, limit)
	if err != nil {
		return 0, fmt.Errorf("retention: attendee checks: %w", err)
	}
	return int(tag.RowsAffected()), nil
}
