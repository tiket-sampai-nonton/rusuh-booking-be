package rabbitmq_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"
	pkgrabbitmq "github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/rabbitmq"

	mock_outbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/_mock/queue/port/outbound"
	queuerabbitmq "github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/infrastructure/rabbitmq"
)

func delivery(t *testing.T, routingKey string, payload any) pkgrabbitmq.Delivery {
	t.Helper()
	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	return pkgrabbitmq.Delivery{
		RoutingKey: routingKey,
		Envelope:   pkgrabbitmq.Envelope{Payload: raw},
		Attempt:    1,
	}
}

func newConsumer(t *testing.T) (*queuerabbitmq.Consumer, *mock_outbound.MockIQueueStore) {
	t.Helper()
	ctrl := gomock.NewController(t)
	store := mock_outbound.NewMockIQueueStore(ctrl)
	return queuerabbitmq.NewConsumer(store, clock.Fixed{T: time.Unix(0, 0)}), store
}

func TestConsumer_JourneyEnded(t *testing.T) {
	c, store := newConsumer(t)
	store.EXPECT().EndEntry(gomock.Any(), "ev1", "e1", "PAID", gomock.Any()).Return("COMPLETED", nil)
	err := c.Subscription().Handler(context.Background(), delivery(t, "booking.journey.ended",
		map[string]string{"eventId": "ev1", "queueEntryId": "e1", "outcome": "PAID"}))
	assert.NoError(t, err)
}

func TestConsumer_JourneyEnded_DoubleDeliveryIdempotent(t *testing.T) {
	c, store := newConsumer(t)
	// end_entry.lua is idempotent; the consumer just calls it each time.
	store.EXPECT().EndEntry(gomock.Any(), "ev1", "e1", "PAID", gomock.Any()).Return("COMPLETED", nil).Times(2)
	d := delivery(t, "booking.journey.ended", map[string]string{"eventId": "ev1", "queueEntryId": "e1", "outcome": "PAID"})
	require.NoError(t, c.Subscription().Handler(context.Background(), d))
	require.NoError(t, c.Subscription().Handler(context.Background(), d))
}

func TestConsumer_QuotaExhausted(t *testing.T) {
	c, store := newConsumer(t)
	store.EXPECT().AddQuotaExhausted(gomock.Any(), "ev1", "deadbeef").Return(nil)
	err := c.Subscription().Handler(context.Background(), delivery(t, "booking.quota.exhausted",
		map[string]string{"eventId": "ev1", "buyerNikHash": "deadbeef"}))
	assert.NoError(t, err)
}

func TestConsumer_SoldOut(t *testing.T) {
	c, store := newConsumer(t)
	store.EXPECT().SetSale(gomock.Any(), "ev1", "CLOSED", true).Return(nil)
	err := c.Subscription().Handler(context.Background(), delivery(t, "booking.sold_out",
		map[string]string{"eventId": "ev1"}))
	assert.NoError(t, err)
}

func TestConsumer_CorruptPayloadPermanent(t *testing.T) {
	c, _ := newConsumer(t)
	d := pkgrabbitmq.Delivery{RoutingKey: "booking.journey.ended", Envelope: pkgrabbitmq.Envelope{Payload: []byte("{not json")}}
	err := c.Subscription().Handler(context.Background(), d)
	require.Error(t, err)
	assert.True(t, pkgrabbitmq.IsPermanent(err), "corrupt payload must be permanent (straight to DLQ)")
}

func TestConsumer_MissingFieldsPermanent(t *testing.T) {
	c, _ := newConsumer(t)
	err := c.Subscription().Handler(context.Background(), delivery(t, "booking.journey.ended",
		map[string]string{"eventId": "ev1"})) // missing entry + outcome
	require.Error(t, err)
	assert.True(t, pkgrabbitmq.IsPermanent(err))
}

func TestConsumer_UnknownRoutingKeyPermanent(t *testing.T) {
	c, _ := newConsumer(t)
	err := c.Subscription().Handler(context.Background(), delivery(t, "booking.unknown", map[string]string{}))
	require.Error(t, err)
	assert.True(t, pkgrabbitmq.IsPermanent(err))
}

func TestConsumer_SubscriptionBindings(t *testing.T) {
	c, _ := newConsumer(t)
	sub := c.Subscription()
	assert.Equal(t, "queue.booking-events", sub.Queue)
	assert.ElementsMatch(t, []string{"booking.journey.ended", "booking.quota.exhausted", "booking.sold_out"}, sub.RoutingKeys)
}
