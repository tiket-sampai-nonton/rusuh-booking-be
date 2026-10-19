package rabbitmq

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var delays = []time.Duration{5 * time.Second, 30 * time.Second, 2 * time.Minute}

func TestDecide(t *testing.T) {
	transient := errors.New("db timeout")
	tests := []struct {
		name    string
		attempt int
		err     error
		want    Decision
	}{
		{"success acks", 1, nil, Decision{Action: ActionAck}},
		{"first failure → 5s", 1, transient, Decision{Action: ActionRetry, Queue: "q.retry.5s", Delay: 5 * time.Second}},
		{"second failure → 30s", 2, transient, Decision{Action: ActionRetry, Queue: "q.retry.30s", Delay: 30 * time.Second}},
		{"third failure → 2m", 3, transient, Decision{Action: ActionRetry, Queue: "q.retry.2m", Delay: 2 * time.Minute}},
		{"fourth failure → DLQ", 4, transient, Decision{Action: ActionDeadLetter, Queue: "q.dlq"}},
		{"permanent → DLQ at once", 1, Permanent(transient), Decision{Action: ActionDeadLetter, Queue: "q.dlq"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, Decide("q", tt.attempt, tt.err, 3, delays))
		})
	}
}

func TestDecide_ReusesLastDelayAndNoDelays(t *testing.T) {
	d := Decide("q", 5, errors.New("x"), 6, delays)
	assert.Equal(t, "q.retry.2m", d.Queue)
	assert.Equal(t, ActionDeadLetter, Decide("q", 1, errors.New("x"), 3, nil).Action)
}

func TestPermanent(t *testing.T) {
	base := errors.New("bad payload")
	p := Permanent(base)
	assert.True(t, IsPermanent(p))
	assert.True(t, IsPermanent(errors.Join(errors.New("ctx"), p)))
	assert.ErrorIs(t, p, base)
	assert.Contains(t, p.Error(), "permanent")
	assert.False(t, IsPermanent(base))
}

func TestNames(t *testing.T) {
	assert.Equal(t, "booking.payment-succeeded.retry.5s", RetryQueueName("booking.payment-succeeded", 5*time.Second))
	assert.Equal(t, "q.retry.2m", RetryQueueName("q", 2*time.Minute))
	assert.Equal(t, "q.retry.1h", RetryQueueName("q", time.Hour))
	assert.Equal(t, "q.retry.1500ms", RetryQueueName("q", 1500*time.Millisecond))
	assert.Equal(t, "q.dlq", DLQName("q"))
}

func TestAttemptOf(t *testing.T) {
	assert.Equal(t, 1, AttemptOf(nil))
	assert.Equal(t, 3, AttemptOf(amqp.Table{HeaderAttempt: int32(3)}))
	assert.Equal(t, 4, AttemptOf(amqp.Table{HeaderAttempt: int64(4)}))
	assert.Equal(t, 2, AttemptOf(amqp.Table{HeaderAttempt: 2}))
	assert.Equal(t, 1, AttemptOf(amqp.Table{HeaderAttempt: "x"}))
}

func TestParseDelays(t *testing.T) {
	got, err := ParseDelays("5s, 30s,2m,")
	require.NoError(t, err)
	assert.Equal(t, delays, got)
	_, err = ParseDelays("5s,soon")
	assert.Error(t, err)
}

func validCfg() Config {
	return Config{URL: "amqp://guest:secret@localhost:5672/", Exchange: "rusuh.events", ExchangeType: "topic",
		Workers: 2, Prefetch: 10, MaxRetries: 3, RetryDelays: delays, ConfirmWait: 5 * time.Second}
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"valid", func(*Config) {}, ""},
		{"bad url", func(c *Config) { c.URL = "http://x" }, "RABBITMQ_URL"},
		{"no exchange", func(c *Config) { c.Exchange = "" }, "RABBITMQ_EXCHANGE wajib"},
		{"bad type", func(c *Config) { c.ExchangeType = "headers2" }, "RABBITMQ_EXCHANGE_TYPE"},
		{"workers", func(c *Config) { c.Workers = 0 }, "RABBITMQ_WORKERS"},
		{"prefetch", func(c *Config) { c.Prefetch = 0 }, "RABBITMQ_PREFETCH"},
		{"retries", func(c *Config) { c.MaxRetries = 11 }, "RABBITMQ_MAX_RETRIES"},
		{"delays missing", func(c *Config) { c.RetryDelays = nil }, "RABBITMQ_RETRY_DELAYS"},
		{"confirm", func(c *Config) { c.ConfirmWait = 0 }, "RABBITMQ_CONFIRM_TIMEOUT"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := validCfg()
			tt.mutate(&c)
			err := c.Validate()
			if tt.want == "" {
				assert.NoError(t, err)
				return
			}
			assert.ErrorContains(t, err, tt.want)
		})
	}
}

func TestRedacted(t *testing.T) {
	assert.NotContains(t, validCfg().Redacted(), "secret")
	assert.Equal(t, "invalid-url", Config{URL: "://"}.Redacted())
}

func TestEnvelope(t *testing.T) {
	at := time.Date(2026, 12, 25, 3, 5, 12, 0, time.FixedZone("WIB", 7*3600))
	env, err := NewEnvelope("booking.paid", "booking-service", "bk-1", "corr-1", map[string]string{"bookingId": "bk-1"}, at)
	require.NoError(t, err)
	assert.NoError(t, env.Validate())
	assert.Len(t, env.MessageID, 36)
	assert.Equal(t, time.UTC, env.OccurredAt.Location())

	var payload map[string]string
	require.NoError(t, json.Unmarshal(env.Payload, &payload))
	assert.Equal(t, "bk-1", payload["bookingId"])

	assert.ErrorContains(t, Envelope{}.Validate(), "messageId kosong")
	_, err = NewEnvelope("x", "p", "a", "", func() {}, at)
	assert.Error(t, err)
	assert.NotEqual(t, NewID(), NewID())
}

func TestSafeCall_RecoversPanic(t *testing.T) {
	err := safeCall(context.Background(), func(context.Context, Delivery) error { panic("boom") }, Delivery{})
	assert.True(t, IsPermanent(err))
	assert.NoError(t, safeCall(context.Background(), func(context.Context, Delivery) error { return nil }, Delivery{}))
}

func TestTruncate(t *testing.T) {
	assert.Equal(t, "abc", truncate("abc", 5))
	assert.Equal(t, "ab", truncate("abc", 2))
}
