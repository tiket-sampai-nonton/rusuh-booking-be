package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/postgres"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/rabbitmq"
)

const (
	defaultBatch   = 100
	maxBackoff     = 5 * time.Minute
	maxErrorLength = 512
	// maxConsecutiveFailures stops a batch early when the broker is down, instead of waiting for the
	// confirm timeout on every row while holding their locks.
	maxConsecutiveFailures = 3
)

// Publisher is the part of *rabbitmq.Publisher the relay needs. Publish returns only after the broker
// confirms the message.
type Publisher interface {
	Publish(ctx context.Context, routingKey string, env rabbitmq.Envelope) error
}

// Relay publishes pending outbox rows. It is safe to run in several worker instances: rows are
// claimed with FOR UPDATE SKIP LOCKED, so one row is handled by one instance per cycle.
type Relay struct {
	db       postgres.TxBeginner
	pub      Publisher
	producer string
	batch    int
	log      *slog.Logger
}

// NewRelay returns a relay for one service; producer is the envelope producer (e.g. "payment-service").
func NewRelay(db postgres.TxBeginner, pub Publisher, producer string, logger *slog.Logger) *Relay {
	return &Relay{db: db, pub: pub, producer: producer, batch: defaultBatch, log: logger}
}

type row struct {
	id            string
	routingKey    string
	aggregateID   string
	schemaVersion int
	payload       json.RawMessage
	occurredAt    time.Time
	attempts      int
}

// RunOnce publishes one batch and returns how many rows were published. A failed publish keeps the row
// pending with a later available_at (exponential backoff with jitter); it is never dropped.
//
// The batch runs in one transaction so row locks are held while publishing. That is bounded by
// batch × RABBITMQ_CONFIRM_TIMEOUT. A crash after publish but before commit republishes the row with the
// same messageId; consumers are idempotent (ADR-010).
func (r *Relay) RunOnce(ctx context.Context) (published int, err error) {
	err = postgres.WithTx(ctx, r.db, pgx.TxOptions{}, func(tx pgx.Tx) error {
		rows, err := r.claim(ctx, tx)
		if err != nil {
			return err
		}
		failures := 0
		for _, row := range rows {
			if ctx.Err() != nil || failures >= maxConsecutiveFailures {
				return nil // commit what is already marked
			}
			env := rabbitmq.Envelope{
				MessageID: row.id, EventType: row.routingKey, SchemaVersion: row.schemaVersion,
				OccurredAt: row.occurredAt.UTC(), Producer: r.producer,
				AggregateID: row.aggregateID, CorrelationID: row.aggregateID, Payload: row.payload,
			}
			if perr := r.pub.Publish(ctx, row.routingKey, env); perr != nil {
				r.log.Warn("outbox publish gagal", "id", row.id, "routingKey", row.routingKey, "attempts", row.attempts+1, "error", perr)
				if err := markFailed(ctx, tx, row, perr); err != nil {
					return err
				}
				failures++
				continue
			}
			failures = 0
			if _, err := tx.Exec(ctx, `UPDATE outbox_events SET published_at = now() WHERE id = $1::uuid`, row.id); err != nil {
				return fmt.Errorf("outbox: mark published: %w", err)
			}
			published++
		}
		return nil
	})
	return published, err
}

func (r *Relay) claim(ctx context.Context, tx pgx.Tx) ([]row, error) {
	rs, err := tx.Query(ctx, `
		SELECT id::text, routing_key, aggregate_id::text, schema_version, payload, occurred_at, publish_attempts
		  FROM outbox_events
		 WHERE published_at IS NULL AND available_at <= now()
		 ORDER BY available_at, occurred_at
		 LIMIT $1
		   FOR UPDATE SKIP LOCKED`, r.batch)
	if err != nil {
		return nil, fmt.Errorf("outbox: claim: %w", err)
	}
	defer rs.Close()
	var out []row
	for rs.Next() {
		var x row
		if err := rs.Scan(&x.id, &x.routingKey, &x.aggregateID, &x.schemaVersion, &x.payload, &x.occurredAt, &x.attempts); err != nil {
			return nil, fmt.Errorf("outbox: scan: %w", err)
		}
		out = append(out, x)
	}
	return out, rs.Err()
}

func markFailed(ctx context.Context, tx pgx.Tx, x row, perr error) error {
	msg := perr.Error()
	if len(msg) > maxErrorLength {
		msg = msg[:maxErrorLength]
	}
	if _, err := tx.Exec(ctx, `
		UPDATE outbox_events
		   SET publish_attempts = publish_attempts + 1, last_error = $2,
		       available_at = now() + make_interval(secs => $3)
		 WHERE id = $1::uuid`, x.id, msg, backoff(x.attempts).Seconds()); err != nil {
		return fmt.Errorf("outbox: mark failed: %w", err)
	}
	return nil
}

// backoff is 1s, 2s, 4s … capped at maxBackoff, with ±20% jitter so instances do not retry in lockstep.
func backoff(attempts int) time.Duration {
	d := time.Second << min(attempts, 9)
	if d > maxBackoff {
		d = maxBackoff
	}
	jitter := 0.8 + 0.4*rand.Float64() // #nosec G404 -- jitter only, not security-sensitive
	return time.Duration(float64(d) * jitter)
}
