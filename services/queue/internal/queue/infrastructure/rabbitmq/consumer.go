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

type quotaExhaustedIn struct {
	EventID      string `json:"eventId"`
	BuyerNikHash string `json:"buyerNikHash"`
}

// handleBookingQuotaExhausted blocks a NIK by adding it to the quota_exhausted set (idempotent SADD).
func (c *Consumer) handleQuotaExhausted(ctx context.Context, raw json.RawMessage) error {
	var in quotaExhaustedIn
	if err := json.Unmarshal(raw, &in); err != nil {
		return pkgrabbitmq.Permanent(fmt.Errorf("consumer: quota.exhausted payload rusak: %w", err))
	}
	if in.EventID == "" || in.BuyerNikHash == "" {
		return pkgrabbitmq.Permanent(fmt.Errorf("consumer: quota.exhausted field wajib kosong"))
	}
	return c.store.AddQuotaExhausted(ctx, in.EventID, in.BuyerNikHash)
}

type soldOutIn struct {
	EventID string `json:"eventId"`
}

// handleBookingSoldOut closes the queue (idempotent HSET).
func (c *Consumer) handleSoldOut(ctx context.Context, raw json.RawMessage) error {
	var in soldOutIn
	if err := json.Unmarshal(raw, &in); err != nil {
		return pkgrabbitmq.Permanent(fmt.Errorf("consumer: sold_out payload rusak: %w", err))
	}
	if in.EventID == "" {
		return pkgrabbitmq.Permanent(fmt.Errorf("consumer: sold_out field wajib kosong"))
	}
	return c.store.SetSale(ctx, in.EventID, "CLOSED", true)
}
