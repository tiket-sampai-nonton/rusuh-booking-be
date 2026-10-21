package rabbitmq_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pkgrabbitmq "github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/rabbitmq"

	bookingrabbitmq "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/infrastructure/rabbitmq"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
)

type fakeSettle struct {
	cmd    inbound.SettleCommand
	called bool
	err    error
}

func (f *fakeSettle) Settle(_ context.Context, cmd inbound.SettleCommand) error {
	f.called = true
	f.cmd = cmd
	return f.err
}

func logger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func deliver(t *testing.T, c *bookingrabbitmq.PaymentConsumer, routingKey string, payload any) error {
	t.Helper()
	sub := c.Subscription()
	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	return sub.Handler(context.Background(), pkgrabbitmq.Delivery{
		Envelope:   pkgrabbitmq.Envelope{Payload: raw, CorrelationID: "cid"},
		RoutingKey: routingKey,
		Attempt:    1,
	})
}

func TestConsumer_SettlesOnSucceeded(t *testing.T) {
	settle := &fakeSettle{}
	c := bookingrabbitmq.NewPaymentConsumer(settle, logger())
	err := deliver(t, c, "payment.succeeded", map[string]any{"bookingId": "b1", "attemptId": "a1", "amountMinor": 1000, "method": "card"})
	require.NoError(t, err)
	assert.True(t, settle.called)
	assert.Equal(t, "b1", settle.cmd.BookingID)
	assert.Equal(t, "card", settle.cmd.Method)
}

func TestConsumer_FailedIsNoop(t *testing.T) {
	settle := &fakeSettle{}
	c := bookingrabbitmq.NewPaymentConsumer(settle, logger())
	err := deliver(t, c, "payment.failed", map[string]any{"bookingId": "b1", "reason": "declined"})
	require.NoError(t, err)
	assert.False(t, settle.called)
}

func TestConsumer_CorruptPayloadIsPermanent(t *testing.T) {
	c := bookingrabbitmq.NewPaymentConsumer(&fakeSettle{}, logger())
	sub := c.Subscription()
	err := sub.Handler(context.Background(), pkgrabbitmq.Delivery{
		Envelope:   pkgrabbitmq.Envelope{Payload: json.RawMessage(`{"bookingId":123}`)}, // wrong type
		RoutingKey: "payment.succeeded",
	})
	require.Error(t, err)
	assert.True(t, pkgrabbitmq.IsPermanent(err))
}

func TestConsumer_MissingBookingIDPermanent(t *testing.T) {
	c := bookingrabbitmq.NewPaymentConsumer(&fakeSettle{}, logger())
	err := deliver(t, c, "payment.succeeded", map[string]any{"attemptId": "a1"})
	require.Error(t, err)
	assert.True(t, pkgrabbitmq.IsPermanent(err))
}
