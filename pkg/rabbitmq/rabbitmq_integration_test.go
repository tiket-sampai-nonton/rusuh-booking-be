//go:build integration

package rabbitmq

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func integrationConn(t *testing.T) *Connection {
	t.Helper()
	url := os.Getenv("TEST_RABBITMQ_URL")
	if url == "" {
		t.Skip("TEST_RABBITMQ_URL tidak diset")
	}
	cfg := Config{URL: url, Exchange: "rusuh.it", ExchangeType: "topic", Workers: 2, Prefetch: 5,
		MaxRetries: 1, RetryDelays: []time.Duration{300 * time.Millisecond}, ConfirmWait: 5 * time.Second}
	c, err := Dial(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	require.NoError(t, c.Ping(context.Background()))
	assert.Equal(t, "rabbitmq", c.Name())
	return c
}

func cleanupQueues(t *testing.T, c *Connection, queue string) {
	t.Cleanup(func() {
		ch, err := c.conn.Channel()
		if err != nil {
			return
		}
		defer func() { _ = ch.Close() }()
		for _, q := range []string{queue, RetryQueueName(queue, 300*time.Millisecond), DLQName(queue)} {
			_, _ = ch.QueueDelete(q, false, false, false)
		}
	})
}

func publish(t *testing.T, c *Connection, key string) Envelope {
	t.Helper()
	pub, err := c.NewPublisher()
	require.NoError(t, err)
	defer func() { _ = pub.Close() }()
	env, err := NewEnvelope("it.event", "it", "agg-1", "corr-1", map[string]int{"n": 1}, time.Now())
	require.NoError(t, err)
	require.NoError(t, pub.Publish(context.Background(), key, env))
	return env
}

func TestConsume_TransientFailureIsRetriedThenAcked(t *testing.T) {
	c := integrationConn(t)
	queue := "it.retry-" + NewID()[:8]
	cleanupQueues(t, c, queue)

	var calls atomic.Int32
	done := make(chan Delivery, 1)
	sub := Subscription{Queue: queue, RoutingKeys: []string{queue + ".v1"}, Handler: func(_ context.Context, d Delivery) error {
		if calls.Add(1) == 1 {
			return errors.New("db sedang sibuk")
		}
		done <- d
		return nil
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = c.Consume(ctx, sub, slog.New(slog.NewTextHandler(io.Discard, nil))) }()
	time.Sleep(300 * time.Millisecond) // topologi terpasang sebelum publish

	env := publish(t, c, queue+".v1")
	select {
	case d := <-done:
		assert.Equal(t, env.MessageID, d.Envelope.MessageID)
		assert.Equal(t, 2, d.Attempt)
	case <-time.After(10 * time.Second):
		t.Fatal("pesan tidak diproses ulang setelah retry")
	}
}

func TestConsume_PermanentFailureGoesToDLQ(t *testing.T) {
	c := integrationConn(t)
	queue := "it.dlq-" + NewID()[:8]
	cleanupQueues(t, c, queue)

	sub := Subscription{Queue: queue, RoutingKeys: []string{queue + ".v1"}, Handler: func(context.Context, Delivery) error {
		return Permanent(errors.New("schemaVersion tidak dikenal"))
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = c.Consume(ctx, sub, slog.New(slog.NewTextHandler(io.Discard, nil))) }()
	time.Sleep(300 * time.Millisecond)

	env := publish(t, c, queue+".v1")
	ch, err := c.conn.Channel()
	require.NoError(t, err)
	defer func() { _ = ch.Close() }()
	assert.Eventually(t, func() bool {
		msg, ok, err := ch.Get(DLQName(queue), true)
		return err == nil && ok && msg.MessageId == env.MessageID && msg.Headers[HeaderLastError] != nil
	}, 10*time.Second, 100*time.Millisecond)
}
