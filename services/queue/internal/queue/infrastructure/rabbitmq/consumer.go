package rabbitmq

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"
	pkgrabbitmq "github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/rabbitmq"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// Routing keys consumed from Booking (design §6.2).
const (
	routingBookingJourneyEnded = "booking.journey.ended"
	routingBookingQuota        = "booking.quota.exhausted"
	routingBookingSoldOut      = "booking.sold_out"
	queueName                  = "queue.booking-events"
)

// SoldOutBatchSize bounds the entries ended per store call; SoldOutMaxBatches bounds the calls per delivery.
const (
	SoldOutBatchSize  = 500
	SoldOutMaxBatches = 20
)

// Consumer handles Booking events. Idempotency comes from guard state + SADD + Lua (no inbox table).
type Consumer struct {
	store outbound.IQueueStore
	clock clock.IClock
}

// NewConsumer accepts interfaces and returns the concrete consumer.
func NewConsumer(store outbound.IQueueStore, clk clock.IClock) *Consumer {
	return &Consumer{store: store, clock: clk}
}

// Subscription binds the queue to the three Booking routing keys and the dispatch handler.
func (c *Consumer) Subscription() pkgrabbitmq.Subscription {
	return pkgrabbitmq.Subscription{
		Queue:       queueName,
		RoutingKeys: []string{routingBookingJourneyEnded, routingBookingQuota, routingBookingSoldOut},
		Handler:     c.handle,
	}
}

func (c *Consumer) handle(ctx context.Context, d pkgrabbitmq.Delivery) error {
	switch d.RoutingKey {
	case routingBookingJourneyEnded:
		return c.handleJourneyEnded(ctx, d.Envelope.Payload)
	case routingBookingQuota:
		return c.handleQuotaExhausted(ctx, d.Envelope.Payload)
	case routingBookingSoldOut:
		return c.handleSoldOut(ctx, d.Envelope.Payload)
	default:
		return pkgrabbitmq.Permanent(fmt.Errorf("consumer: routing key tak dikenal %q", d.RoutingKey))
	}
}

type journeyEndedIn struct {
	EventID      string `json:"eventId"`
	QueueEntryID string `json:"queueEntryId"`
	Outcome      string `json:"outcome"`
}

// handleBookingJourneyEnded releases the slot and finalizes the entry idempotently (end_entry.lua).
func (c *Consumer) handleJourneyEnded(ctx context.Context, raw json.RawMessage) error {
	var in journeyEndedIn
	if err := json.Unmarshal(raw, &in); err != nil {
		return pkgrabbitmq.Permanent(fmt.Errorf("consumer: journey.ended payload rusak: %w", err))
	}
	if in.EventID == "" || in.QueueEntryID == "" || in.Outcome == "" {
		return pkgrabbitmq.Permanent(fmt.Errorf("consumer: journey.ended field wajib kosong"))
	}
	_, err := c.store.EndEntry(ctx, in.EventID, in.QueueEntryID, in.Outcome, c.clock.Now().UnixMilli())
	return err
}

// handleQuotaExhausted is a no-op acknowledge kept for ONE release (ADR-030). Booking no longer emits
// booking.quota.exhausted at reserve (the buyer quota is checked at hold), but pods of the previous version may
// still publish it during the rollout. Acknowledging without effect avoids a DLQ storm, and, unlike before, never
// ends a live buyer. Remove the binding and this handler in the next release.
func (c *Consumer) handleQuotaExhausted(_ context.Context, _ json.RawMessage) error { return nil }

type soldOutIn struct {
	EventID string `json:"eventId"`
}

// handleSoldOut closes the sale (idempotent HSET) and ends the waiting set with SOLD_OUT (idempotent, batched).
func (c *Consumer) handleSoldOut(ctx context.Context, raw json.RawMessage) error {
	var in soldOutIn
	if err := json.Unmarshal(raw, &in); err != nil {
		return pkgrabbitmq.Permanent(fmt.Errorf("consumer: sold_out payload rusak: %w", err))
	}
	if in.EventID == "" {
		return pkgrabbitmq.Permanent(fmt.Errorf("consumer: sold_out field wajib kosong"))
	}
	if err := c.store.SetSale(ctx, in.EventID, "CLOSED", true); err != nil {
		return err
	}
	// Req 5.5 / SRS-F-10-02: actively end every WAITING/STALE entry with SOLD_OUT so clients stop waiting.
	// ADMITTED/CLAIMED are untouched (Jangan PHP). Batched and capped so one huge waiting set cannot hold the
	// consumer; past the cap a retryable error redelivers and the idempotent drain continues.
	for i := 0; i < SoldOutMaxBatches; i++ {
		res, err := c.store.EndWaitingSoldOut(ctx, in.EventID, c.clock.Now().UnixMilli(), SoldOutBatchSize)
		if err != nil {
			return err
		}
		if res.Scanned < SoldOutBatchSize {
			return nil
		}
	}
	return fmt.Errorf("consumer: sold_out masih menyisakan antrean setelah %d batch, ulangi", SoldOutMaxBatches)
}
