package rabbitmq_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	pkgrabbitmq "github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/rabbitmq"

	mock_inbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/_mock/notification/port/inbound"
	notificationmq "github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/infrastructure/rabbitmq"
)

func logger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func deliver(t *testing.T, sub pkgrabbitmq.Subscription, key string, payload any) error {
	t.Helper()
	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	return sub.Handler(context.Background(), pkgrabbitmq.Delivery{
		Envelope:   pkgrabbitmq.Envelope{MessageID: "m1", Payload: raw},
		RoutingKey: key,
	})
}

func TestBookingConsumer_DispatchesByRoutingKey(t *testing.T) {
	ctrl := gomock.NewController(t)
	paid := mock_inbound.NewMockINotifyBookingPaid(ctrl)
	expired := mock_inbound.NewMockINotifyBookingExpired(ctrl)
	review := mock_inbound.NewMockINotifyBookingReview(ctrl)
	c := notificationmq.NewBookingConsumer(paid, expired, review, logger())
	sub := c.Subscription()

	assert.Equal(t, notificationmq.QueueBookingEvents, sub.Queue)

	paid.EXPECT().NotifyBookingPaid(gomock.Any(), "m1", gomock.Any()).Return(nil)
	require.NoError(t, deliver(t, sub, "booking.paid", map[string]any{"bookingId": "b"}))

	expired.EXPECT().NotifyBookingExpired(gomock.Any(), "m1", gomock.Any()).Return(nil)
	require.NoError(t, deliver(t, sub, "booking.expired", map[string]any{"bookingId": "b"}))

	review.EXPECT().NotifyBookingReview(gomock.Any(), "m1", gomock.Any()).Return(nil)
	require.NoError(t, deliver(t, sub, "booking.review", map[string]any{"bookingId": "b"}))
}

func TestBookingConsumer_CorruptPayloadPermanent(t *testing.T) {
	ctrl := gomock.NewController(t)
	c := notificationmq.NewBookingConsumer(
		mock_inbound.NewMockINotifyBookingPaid(ctrl),
		mock_inbound.NewMockINotifyBookingExpired(ctrl),
		mock_inbound.NewMockINotifyBookingReview(ctrl), logger())
	sub := c.Subscription()

	err := sub.Handler(context.Background(), pkgrabbitmq.Delivery{
		Envelope:   pkgrabbitmq.Envelope{MessageID: "m1", Payload: json.RawMessage(`{"bookingId":123}`)},
		RoutingKey: "booking.paid",
	})
	require.Error(t, err)
	assert.True(t, pkgrabbitmq.IsPermanent(err))
}

func TestBookingConsumer_UnknownKeyAcks(t *testing.T) {
	ctrl := gomock.NewController(t)
	c := notificationmq.NewBookingConsumer(
		mock_inbound.NewMockINotifyBookingPaid(ctrl),
		mock_inbound.NewMockINotifyBookingExpired(ctrl),
		mock_inbound.NewMockINotifyBookingReview(ctrl), logger())
	require.NoError(t, deliver(t, c.Subscription(), "booking.unknown", map[string]any{}))
}

func TestTurnConsumer_Dispatches(t *testing.T) {
	ctrl := gomock.NewController(t)
	soon := mock_inbound.NewMockINotifyTurnSoon(ctrl)
	arrived := mock_inbound.NewMockINotifyTurnArrived(ctrl)
	c := notificationmq.NewTurnConsumer(soon, arrived, logger())
	sub := c.Subscription()
	assert.Equal(t, notificationmq.QueueQueueEvents, sub.Queue)

	soon.EXPECT().NotifyTurnSoon(gomock.Any(), "m1", gomock.Any()).Return(nil)
	require.NoError(t, deliver(t, sub, "queue.turn.soon", map[string]any{"eventId": "e"}))

	arrived.EXPECT().NotifyTurnArrived(gomock.Any(), "m1", gomock.Any()).Return(nil)
	require.NoError(t, deliver(t, sub, "queue.turn.arrived", map[string]any{"eventId": "e"}))
}

func TestOTPConsumer_Dispatches(t *testing.T) {
	ctrl := gomock.NewController(t)
	otp := mock_inbound.NewMockINotifyOTPRequested(ctrl)
	c := notificationmq.NewOTPConsumer(otp, logger())
	sub := c.Subscription()
	assert.Equal(t, notificationmq.QueueOTP, sub.Queue)

	otp.EXPECT().NotifyOTPRequested(gomock.Any(), "m1", gomock.Any()).Return(nil)
	require.NoError(t, deliver(t, sub, "otp.requested", map[string]any{"emailEnc": "x"}))
}

func TestOTPConsumer_CorruptPayloadPermanent(t *testing.T) {
	ctrl := gomock.NewController(t)
	c := notificationmq.NewOTPConsumer(mock_inbound.NewMockINotifyOTPRequested(ctrl), logger())
	err := c.Subscription().Handler(context.Background(), pkgrabbitmq.Delivery{
		Envelope:   pkgrabbitmq.Envelope{MessageID: "m1", Payload: json.RawMessage(`{`)},
		RoutingKey: "otp.requested",
	})
	require.Error(t, err)
	assert.True(t, pkgrabbitmq.IsPermanent(err))
}
