// Package rabbitmq implements the Queue event publisher and consumer using pkg/rabbitmq.
// Event payloads carry no PII (no NIK/email/token) — contract additionalProperties:false (design §6).
package rabbitmq

import (
	"context"
	"fmt"
	"time"

	pkgrabbitmq "github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/rabbitmq"
)

// Routing keys published by the queue service (design §6.1, §7.3).
const (
	routingTurnSoon     = "queue.turn.soon"
	routingTurnArrived  = "queue.turn.arrived"
	routingJourneyEnded = "booking.journey.ended"
	producerName        = "queue-service"
)

// EventPublisher implements outbound.IEventPublisher.
type EventPublisher struct {
	pub *pkgrabbitmq.Publisher
	now func() time.Time
}

// NewEventPublisher wraps an existing confirm-mode publisher.
func NewEventPublisher(pub *pkgrabbitmq.Publisher, now func() time.Time) *EventPublisher {
	return &EventPublisher{pub: pub, now: now}
}

type turnSoonPayload struct {
	EventID      string `json:"eventId"`
	QueueEntryID string `json:"queueEntryId"`
	VisitorID    string `json:"visitorId"`
}

type turnArrivedPayload struct {
	EventID       string `json:"eventId"`
	QueueEntryID  string `json:"queueEntryId"`
	VisitorID     string `json:"visitorId"`
	ClaimDeadline string `json:"claimDeadline"`
}

type journeyEndedPayload struct {
	EventID      string `json:"eventId"`
	QueueEntryID string `json:"queueEntryId"`
	Outcome      string `json:"outcome"`
}

// PublishTurnSoon notifies that a position threshold was reached (design §6.1).
func (p *EventPublisher) PublishTurnSoon(ctx context.Context, eventID, queueEntryID, visitorID string) error {
	return p.publish(ctx, routingTurnSoon, eventID, turnSoonPayload{EventID: eventID, QueueEntryID: queueEntryID, VisitorID: visitorID})
}

// PublishTurnArrived notifies that an entry was admitted (design §6.1).
func (p *EventPublisher) PublishTurnArrived(ctx context.Context, eventID, queueEntryID, visitorID string, claimDeadline time.Time) error {
	return p.publish(ctx, routingTurnArrived, eventID, turnArrivedPayload{
		EventID: eventID, QueueEntryID: queueEntryID, VisitorID: visitorID,
		ClaimDeadline: claimDeadline.UTC().Format(time.RFC3339),
	})
}

// PublishJourneyEnded tells Booking to release a reservation (design §7.3), e.g. outcome UNCLAIMED.
func (p *EventPublisher) PublishJourneyEnded(ctx context.Context, eventID, queueEntryID, outcome string) error {
	return p.publish(ctx, routingJourneyEnded, eventID, journeyEndedPayload{EventID: eventID, QueueEntryID: queueEntryID, Outcome: outcome})
}

func (p *EventPublisher) publish(ctx context.Context, routingKey, aggregateID string, payload any) error {
	env, err := pkgrabbitmq.NewEnvelope(routingKey, producerName, aggregateID, "", payload, p.now())
	if err != nil {
		return fmt.Errorf("queue publisher: build envelope %s: %w", routingKey, err)
	}
	if err := p.pub.Publish(ctx, routingKey, env); err != nil {
		return fmt.Errorf("queue publisher: publish %s: %w", routingKey, err)
	}
	return nil
}
