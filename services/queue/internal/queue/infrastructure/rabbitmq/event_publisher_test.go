package rabbitmq_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	queuerabbitmq "github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/infrastructure/rabbitmq"
)

func TestEventPublisher_NewPublisher(t *testing.T) {
	p := queuerabbitmq.NewEventPublisher(nil, time.Now)
	assert.NotNil(t, p)
}

func TestEventPublisher_PublishTurnSoon_NilPubPanics(t *testing.T) {
	p := queuerabbitmq.NewEventPublisher(nil, time.Now)
	assert.Panics(t, func() {
		_ = p.PublishTurnSoon(context.Background(), "ev1", "e1", "v1")
	})
}

func TestEventPublisher_PublishTurnArrived_NilPubPanics(t *testing.T) {
	p := queuerabbitmq.NewEventPublisher(nil, time.Now)
	assert.Panics(t, func() {
		_ = p.PublishTurnArrived(context.Background(), "ev1", "e1", "v1", time.Now())
	})
}

func TestEventPublisher_PublishJourneyEnded_NilPubPanics(t *testing.T) {
	p := queuerabbitmq.NewEventPublisher(nil, time.Now)
	assert.Panics(t, func() {
		_ = p.PublishJourneyEnded(context.Background(), "ev1", "e1", "UNCLAIMED")
	})
}
