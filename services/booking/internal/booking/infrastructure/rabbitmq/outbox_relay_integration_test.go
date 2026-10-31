//go:build integration

package rabbitmq_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pkgrabbitmq "github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/rabbitmq"

	bookingrabbitmq "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/infrastructure/rabbitmq"
)

type recordingStore struct {
	rows      []bookingrabbitmq.OutboxRow
	published []string
	failed    map[string]time.Duration
}

func (s *recordingStore) Pending(context.Context, int) ([]bookingrabbitmq.OutboxRow, error) {
	return s.rows, nil
}
func (s *recordingStore) MarkPublished(_ context.Context, id string) error {
	s.published = append(s.published, id)
	return nil
}
func (s *recordingStore) MarkFailed(_ context.Context, id string, _ int, _ string, b time.Duration) error {
	if s.failed == nil {
		s.failed = map[string]time.Duration{}
	}
	s.failed[id] = b
	return nil
}

func TestOutboxRelay_PublishesToTheBrokerAndBacksOffOnFailure(t *testing.T) {
	url := os.Getenv("TEST_RABBITMQ_URL")
	if url == "" {
		t.Skip("TEST_RABBITMQ_URL tidak diset")
	}
	conn, err := pkgrabbitmq.Dial(pkgrabbitmq.Config{URL: url, Exchange: "rusuh.it", ExchangeType: "topic", Workers: 1, Prefetch: 1,
		MaxRetries: 1, RetryDelays: []time.Duration{time.Second}, ConfirmWait: 5 * time.Second})
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	pub, err := conn.NewPublisher()
	require.NoError(t, err)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	store := &recordingStore{rows: []bookingrabbitmq.OutboxRow{
		{ID: "ok", RoutingKey: "it.booking.relay", AggregateID: "agg-1", SchemaVersion: 1, Payload: json.RawMessage(`{"a":1}`)},
		{ID: "zero-version", RoutingKey: "it.booking.relay", AggregateID: "agg-2", SchemaVersion: 0, Payload: json.RawMessage(`{"a":2}`)},
		{ID: "bad-payload", RoutingKey: "it.booking.relay", AggregateID: "agg-3", SchemaVersion: 1, Payload: json.RawMessage(`{bukan json`), Attempts: 2},
	}}
	relay := bookingrabbitmq.NewOutboxRelay(store, pub, time.Now, logger)
	require.NoError(t, relay.RunOnce(context.Background()))
	assert.Equal(t, []string{"ok", "zero-version"}, store.published)
	assert.Equal(t, 4*time.Second, store.failed["bad-payload"], "backoff eksponensial: 2^attempts detik")

	// A closed publisher cannot deliver: the row is kept for retry with a backoff, capped for many attempts.
	require.NoError(t, pub.Close())
	store2 := &recordingStore{rows: []bookingrabbitmq.OutboxRow{
		{ID: "late", RoutingKey: "it.booking.relay", AggregateID: "agg-4", SchemaVersion: 1, Payload: json.RawMessage(`{"a":4}`), Attempts: 30},
	}}
	require.NoError(t, bookingrabbitmq.NewOutboxRelay(store2, pub, time.Now, logger).RunOnce(context.Background()))
	assert.Empty(t, store2.published)
	assert.Equal(t, 64*time.Second, store2.failed["late"], "tertinggi 2^6 detik")
}
