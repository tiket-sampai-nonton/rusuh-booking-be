//go:build e2e

package helpers

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

const exchange = "rusuh.events"

// InjectPaymentSucceeded publishes a payment.succeeded message directly to the booking exchange,
// simulating what the Payment Service sends after a successful gateway callback.
func InjectPaymentSucceeded(amqpURL, bookingID, method string, settlementTime time.Time) error {
	conn, err := amqp.Dial(amqpURL)
	if err != nil {
		return fmt.Errorf("amqp dial: %w", err)
	}
	defer conn.Close()

	ch, err := conn.Channel()
	if err != nil {
		return fmt.Errorf("amqp channel: %w", err)
	}
	defer ch.Close()

	payload, _ := json.Marshal(map[string]any{
		"bookingId":      bookingID,
		"attemptId":      "e2e-test-attempt",
		"settlementTime": settlementTime.UTC().Format(time.RFC3339),
		"amountMinor":    150000,
		"method":         method,
	})

	return ch.PublishWithContext(context.Background(),
		exchange,
		"payment.succeeded",
		false, false,
		amqp.Publishing{
			ContentType:   "application/json",
			CorrelationId: bookingID,
			Body:          payload,
		},
	)
}
