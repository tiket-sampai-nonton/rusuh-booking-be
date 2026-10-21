// Package rabbitmq implements the Booking consumer (payment.*) and the outbox relay publisher.
package rabbitmq

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	pkgrabbitmq "github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/rabbitmq"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
)

// Routing keys consumed from the Payment Service (design §8.1).
const (
	routingPaymentSucceeded = "payment.succeeded"
	routingPaymentFailed    = "payment.failed"
	queuePaymentEvents      = "booking.payment-events"
)

// PaymentConsumer handles payment.succeeded (settlement) and payment.failed (no-op). It satisfies the
// consumer contract: idempotent via the settlement state guard; corrupt payload → Permanent → DLQ.
type PaymentConsumer struct {
	settle inbound.ISettlementUsecase
	logger *slog.Logger
}

// NewPaymentConsumer wires the consumer.
func NewPaymentConsumer(settle inbound.ISettlementUsecase, logger *slog.Logger) *PaymentConsumer {
	return &PaymentConsumer{settle: settle, logger: logger}
}

// Subscription describes the queue + routing keys + handler for pkg/rabbitmq.Consume.
func (c *PaymentConsumer) Subscription() pkgrabbitmq.Subscription {
	return pkgrabbitmq.Subscription{
		Queue:       queuePaymentEvents,
		RoutingKeys: []string{routingPaymentSucceeded, routingPaymentFailed},
		Handler:     c.handle,
	}
}

type succeededPayload struct {
	BookingID      string `json:"bookingId"`
	AttemptID      string `json:"attemptId"`
	SettlementTime string `json:"settlementTime"`
	AmountMinor    int64  `json:"amountMinor"`
	Method         string `json:"method"`
}

func (c *PaymentConsumer) handle(ctx context.Context, d pkgrabbitmq.Delivery) error {
	switch d.RoutingKey {
	case routingPaymentSucceeded:
		var p succeededPayload
		if err := json.Unmarshal(d.Envelope.Payload, &p); err != nil {
			return pkgrabbitmq.Permanent(fmt.Errorf("payment.succeeded decode: %w", err))
		}
		if p.BookingID == "" {
			return pkgrabbitmq.Permanent(fmt.Errorf("payment.succeeded tanpa bookingId"))
		}
		return c.settle.Settle(ctx, inbound.SettleCommand{
			BookingID:     p.BookingID,
			AttemptID:     p.AttemptID,
			AmountMinor:   p.AmountMinor,
			Method:        p.Method,
			CorrelationID: d.Envelope.CorrelationID,
		})
	case routingPaymentFailed:
		// Log only; the booking waits for its TTL (AC9.3).
		c.logger.Info("payment.failed diterima", "messageId", d.Envelope.MessageID)
		return nil
	default:
		return nil
	}
}

// OutboxRelay publishes pending outbox rows to the exchange with confirms (transactional outbox).
type OutboxRelay struct {
	store  OutboxStore
	pub    *pkgrabbitmq.Publisher
	now    func() time.Time
	logger *slog.Logger
	batch  int
}

// OutboxStore is the persistence the relay needs (implemented by the postgres OutboxWriter).
type OutboxStore interface {
	Pending(ctx context.Context, limit int) ([]OutboxRow, error)
	MarkPublished(ctx context.Context, id string) error
	MarkFailed(ctx context.Context, id string, attempts int, errMsg string, backoff time.Duration) error
}

// OutboxRow is one pending outbox event.
type OutboxRow struct {
	ID            string
	RoutingKey    string
	AggregateID   string
	SchemaVersion int
	Payload       json.RawMessage
	Attempts      int
}

// NewOutboxRelay wires the relay.
func NewOutboxRelay(store OutboxStore, pub *pkgrabbitmq.Publisher, now func() time.Time, logger *slog.Logger) *OutboxRelay {
	return &OutboxRelay{store: store, pub: pub, now: now, logger: logger, batch: 100}
}

// RunOnce publishes one batch of pending rows. Called every WORKER_INTERVAL.
func (r *OutboxRelay) RunOnce(ctx context.Context) error {
	rows, err := r.store.Pending(ctx, r.batch)
	if err != nil {
		return err
	}
	for _, row := range rows {
		env, err := pkgrabbitmq.NewEnvelope(row.RoutingKey, "booking-service", row.AggregateID, row.AggregateID, row.Payload, r.now())
		if err != nil {
			_ = r.store.MarkFailed(ctx, row.ID, row.Attempts+1, err.Error(), backoff(row.Attempts))
			continue
		}
		env.SchemaVersion = row.SchemaVersion
		if env.SchemaVersion < 1 {
			env.SchemaVersion = 1
		}
		if perr := r.pub.Publish(ctx, row.RoutingKey, env); perr != nil {
			r.logger.Warn("outbox publish gagal", "id", row.ID, "routingKey", row.RoutingKey, "error", perr)
			_ = r.store.MarkFailed(ctx, row.ID, row.Attempts+1, perr.Error(), backoff(row.Attempts))
			continue
		}
		if merr := r.store.MarkPublished(ctx, row.ID); merr != nil {
			return merr
		}
	}
	return nil
}

func backoff(attempts int) time.Duration {
	d := time.Duration(1<<min(attempts, 6)) * time.Second
	if d > 5*time.Minute {
		d = 5 * time.Minute
	}
	return d
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
