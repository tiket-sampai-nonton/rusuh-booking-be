// Package rabbitmq wires the Notification consumers: it decodes each event envelope and dispatches
// to the right usecase by routing key. Corrupt payloads become Permanent (poison → DLQ).
package rabbitmq

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	pkgrabbitmq "github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/rabbitmq"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/port/inbound"
)

// Queue names consumed by Notification.
const (
	QueueBookingEvents = "notification.booking.events"
	QueueQueueEvents   = "notification.queue.events"
	QueueOTP           = "notification.otp"
)

// Routing keys.
const (
	keyBookingPaid    = "booking.paid"
	keyBookingExpired = "booking.expired"
	keyBookingReview  = "booking.review"
	keyTurnSoon       = "queue.turn.soon"
	keyTurnArrived    = "queue.turn.arrived"
	keyOTPRequested   = "otp.requested"
)

// BookingConsumer dispatches booking.* email notifications.
type BookingConsumer struct {
	paid    inbound.INotifyBookingPaid
	expired inbound.INotifyBookingExpired
	review  inbound.INotifyBookingReview
	logger  *slog.Logger
}

// NewBookingConsumer wires the booking consumer.
func NewBookingConsumer(paid inbound.INotifyBookingPaid, expired inbound.INotifyBookingExpired, review inbound.INotifyBookingReview, logger *slog.Logger) *BookingConsumer {
	return &BookingConsumer{paid: paid, expired: expired, review: review, logger: logger}
}

// Subscription describes queue + routing keys + handler for pkg/rabbitmq.Consume.
func (c *BookingConsumer) Subscription() pkgrabbitmq.Subscription {
	return pkgrabbitmq.Subscription{
		Queue:       QueueBookingEvents,
		RoutingKeys: []string{keyBookingPaid, keyBookingExpired, keyBookingReview},
		Handler:     c.handle,
	}
}

func (c *BookingConsumer) handle(ctx context.Context, d pkgrabbitmq.Delivery) error {
	switch d.RoutingKey {
	case keyBookingPaid:
		var p dto.BookingPaidPayload
		if err := json.Unmarshal(d.Envelope.Payload, &p); err != nil {
			return pkgrabbitmq.Permanent(fmt.Errorf("decode booking.paid: %w", err))
		}
		return c.paid.NotifyBookingPaid(ctx, d.Envelope.MessageID, p)
	case keyBookingExpired:
		var p dto.BookingExpiredPayload
		if err := json.Unmarshal(d.Envelope.Payload, &p); err != nil {
			return pkgrabbitmq.Permanent(fmt.Errorf("decode booking.expired: %w", err))
		}
		return c.expired.NotifyBookingExpired(ctx, d.Envelope.MessageID, p)
	case keyBookingReview:
		var p dto.BookingReviewPayload
		if err := json.Unmarshal(d.Envelope.Payload, &p); err != nil {
			return pkgrabbitmq.Permanent(fmt.Errorf("decode booking.review: %w", err))
		}
		return c.review.NotifyBookingReview(ctx, d.Envelope.MessageID, p)
	default:
		c.logger.Warn("routing key tak dikenal di booking.events", "routingKey", d.RoutingKey)
		return nil
	}
}

// TurnConsumer dispatches queue.turn.* push notifications.
type TurnConsumer struct {
	soon    inbound.INotifyTurnSoon
	arrived inbound.INotifyTurnArrived
	logger  *slog.Logger
}

// NewTurnConsumer wires the turn consumer.
func NewTurnConsumer(soon inbound.INotifyTurnSoon, arrived inbound.INotifyTurnArrived, logger *slog.Logger) *TurnConsumer {
	return &TurnConsumer{soon: soon, arrived: arrived, logger: logger}
}

// Subscription describes queue + routing keys + handler.
func (c *TurnConsumer) Subscription() pkgrabbitmq.Subscription {
	return pkgrabbitmq.Subscription{
		Queue:       QueueQueueEvents,
		RoutingKeys: []string{keyTurnSoon, keyTurnArrived},
		Handler:     c.handle,
	}
}

func (c *TurnConsumer) handle(ctx context.Context, d pkgrabbitmq.Delivery) error {
	switch d.RoutingKey {
	case keyTurnSoon:
		var p dto.TurnSoonPayload
		if err := json.Unmarshal(d.Envelope.Payload, &p); err != nil {
			return pkgrabbitmq.Permanent(fmt.Errorf("decode queue.turn.soon: %w", err))
		}
		return c.soon.NotifyTurnSoon(ctx, d.Envelope.MessageID, p)
	case keyTurnArrived:
		var p dto.TurnArrivedPayload
		if err := json.Unmarshal(d.Envelope.Payload, &p); err != nil {
			return pkgrabbitmq.Permanent(fmt.Errorf("decode queue.turn.arrived: %w", err))
		}
		return c.arrived.NotifyTurnArrived(ctx, d.Envelope.MessageID, p)
	default:
		c.logger.Warn("routing key tak dikenal di queue.events", "routingKey", d.RoutingKey)
		return nil
	}
}

// OTPConsumer dispatches otp.requested login emails (no retry queue — time-sensitive).
type OTPConsumer struct {
	otp    inbound.INotifyOTPRequested
	logger *slog.Logger
}

// NewOTPConsumer wires the OTP consumer.
func NewOTPConsumer(otp inbound.INotifyOTPRequested, logger *slog.Logger) *OTPConsumer {
	return &OTPConsumer{otp: otp, logger: logger}
}

// Subscription describes queue + routing keys + handler.
func (c *OTPConsumer) Subscription() pkgrabbitmq.Subscription {
	return pkgrabbitmq.Subscription{
		Queue:       QueueOTP,
		RoutingKeys: []string{keyOTPRequested},
		Handler:     c.handle,
	}
}

func (c *OTPConsumer) handle(ctx context.Context, d pkgrabbitmq.Delivery) error {
	if d.RoutingKey != keyOTPRequested {
		c.logger.Warn("routing key tak dikenal di otp", "routingKey", d.RoutingKey)
		return nil
	}
	var p dto.OTPRequestedPayload
	if err := json.Unmarshal(d.Envelope.Payload, &p); err != nil {
		return pkgrabbitmq.Permanent(fmt.Errorf("decode otp.requested: %w", err))
	}
	return c.otp.NotifyOTPRequested(ctx, d.Envelope.MessageID, p)
}
