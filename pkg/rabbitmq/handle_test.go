package rabbitmq

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeAck struct{ acked, nacked, requeue bool }

func (f *fakeAck) Ack(uint64, bool) error { f.acked = true; return nil }
func (f *fakeAck) Nack(_ uint64, _ bool, requeue bool) error {
	f.nacked, f.requeue = true, requeue
	return nil
}
func (f *fakeAck) Reject(uint64, bool) error { return nil }

type fakePub struct {
	key string
	msg amqp.Publishing
	err error
}

func (f *fakePub) publishRaw(_ context.Context, _ string, key string, msg amqp.Publishing) error {
	f.key, f.msg = key, msg
	return f.err
}

func delivery(t *testing.T, ack *fakeAck, body []byte, attempt int) amqp.Delivery {
	t.Helper()
	h := amqp.Table{}
	if attempt > 0 {
		h[HeaderAttempt] = int32(attempt)
	}
	return amqp.Delivery{Acknowledger: ack, Body: body, Headers: h, MessageId: "m-1", RoutingKey: "payment.succeeded.v1"}
}

func validBody(t *testing.T) []byte {
	t.Helper()
	env, err := NewEnvelope("payment.succeeded", "payment-service", "bk-1", "c", map[string]int{"n": 1}, time.Now())
	require.NoError(t, err)
	b, err := json.Marshal(env)
	require.NoError(t, err)
	return b
}

func TestHandle(t *testing.T) {
	c := &Connection{cfg: validCfg()}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.Background()

	tests := []struct {
		name        string
		body        []byte
		attempt     int
		handlerErr  error
		pubErr      error
		wantAck     bool
		wantRequeue bool
		wantTarget  string
		wantAttempt int32
	}{
		{"success", validBody(t), 0, nil, nil, true, false, "", 0},
		{"transient → retry 5s", validBody(t), 0, errors.New("db"), nil, true, false, "q.retry.5s", 2},
		{"third transient → retry 2m", validBody(t), 3, errors.New("db"), nil, true, false, "q.retry.2m", 4},
		{"exhausted → dlq", validBody(t), 4, errors.New("db"), nil, true, false, "q.dlq", 5},
		{"permanent → dlq", validBody(t), 0, Permanent(errors.New("bad")), nil, true, false, "q.dlq", 2},
		{"bad json → dlq", []byte("{"), 0, nil, nil, true, false, "q.dlq", 2},
		{"invalid envelope → dlq", []byte(`{"messageId":""}`), 0, nil, nil, true, false, "q.dlq", 2},
		{"republish fails → requeue", validBody(t), 0, errors.New("db"), errors.New("broker"), false, true, "q.retry.5s", 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ack := &fakeAck{}
			pub := &fakePub{err: tt.pubErr}
			var got Delivery
			sub := Subscription{Queue: "q", Handler: func(_ context.Context, d Delivery) error { got = d; return tt.handlerErr }}
			c.handle(ctx, pub, sub, delivery(t, ack, tt.body, tt.attempt), log)

			assert.Equal(t, tt.wantAck, ack.acked)
			assert.Equal(t, tt.wantRequeue, ack.requeue)
			assert.Equal(t, tt.wantTarget, pub.key)
			if tt.wantTarget != "" {
				assert.Equal(t, tt.wantAttempt, pub.msg.Headers[HeaderAttempt])
				assert.Equal(t, "q", pub.msg.Headers[HeaderOrigQueue])
				assert.NotEmpty(t, pub.msg.Headers[HeaderLastError])
			}
			if tt.name == "success" {
				assert.Equal(t, "payment.succeeded.v1", got.RoutingKey)
				assert.Equal(t, 1, got.Attempt)
			}
		})
	}
}
