package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	bookingrabbitmq "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/infrastructure/rabbitmq"
)

// OutboxWriter reads and updates the outbox for the relay. Writes into the outbox happen inside the
// unit-of-work transaction (enqueueOutbox); this type only serves the relay worker.
type OutboxWriter struct{ pool *pgxpool.Pool }

// NewOutboxWriter returns the writer.
func NewOutboxWriter(pool *pgxpool.Pool) *OutboxWriter { return &OutboxWriter{pool: pool} }

// Pending returns unpublished rows ordered by availability.
func (w *OutboxWriter) Pending(ctx context.Context, limit int) ([]bookingrabbitmq.OutboxRow, error) {
	rows, err := w.pool.Query(ctx, `
		SELECT id::text, routing_key, aggregate_id::text, schema_version, payload, publish_attempts
		  FROM booking.outbox_events
		 WHERE published_at IS NULL AND available_at <= now()
		 ORDER BY available_at LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("outbox: pending: %w", err)
	}
	defer rows.Close()
	var out []bookingrabbitmq.OutboxRow
	for rows.Next() {
		var r bookingrabbitmq.OutboxRow
		if err := rows.Scan(&r.ID, &r.RoutingKey, &r.AggregateID, &r.SchemaVersion, &r.Payload, &r.Attempts); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// MarkPublished stamps the row as delivered.
func (w *OutboxWriter) MarkPublished(ctx context.Context, id string) error {
	_, err := w.pool.Exec(ctx, `UPDATE booking.outbox_events SET published_at=now() WHERE id=$1::uuid`, id)
	return err
}

// MarkFailed records a failed publish and backs off.
func (w *OutboxWriter) MarkFailed(ctx context.Context, id string, attempts int, errMsg string, backoff time.Duration) error {
	_, err := w.pool.Exec(ctx, `
		UPDATE booking.outbox_events
		   SET publish_attempts=$2, last_error=$3, available_at=now() + make_interval(secs => $4)
		 WHERE id=$1::uuid`, id, attempts, truncate(errMsg, 512), int(backoff.Seconds()))
	return err
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
