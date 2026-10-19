package rabbitmq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// Header names used by the retry mechanism.
const (
	HeaderAttempt   = "x-rbt-attempt"
	HeaderLastError = "x-rbt-last-error"
	HeaderOrigQueue = "x-rbt-queue"
)

// Delivery is what a handler receives.
type Delivery struct {
	Envelope   Envelope
	RoutingKey string
	Attempt    int // 1 untuk pengiriman pertama
}

// Handler processes one delivery. Handler harus idempoten (consumer at-least-once, SDD §4.5.2).
// Kembalikan Permanent(err) untuk kegagalan yang tidak akan sembuh dengan retry.
type Handler func(ctx context.Context, d Delivery) error

// Subscription binds a queue to routing keys and a handler.
type Subscription struct {
	Queue       string   // mis. booking.payment-succeeded
	RoutingKeys []string // mis. payment.succeeded.v1
	Handler     Handler
}

// DeclareTopology declares the main queue, its bindings, the retry queues (TTL + dead-letter kembali
// ke queue utama lewat default exchange), dan DLQ.
func DeclareTopology(ch *amqp.Channel, cfg Config, sub Subscription) error {
	if _, err := ch.QueueDeclare(sub.Queue, true, false, false, false, amqp.Table{"x-queue-type": "quorum"}); err != nil {
		return fmt.Errorf("rabbitmq: declare %s: %w", sub.Queue, err)
	}
	for _, key := range sub.RoutingKeys {
		if err := ch.QueueBind(sub.Queue, key, cfg.Exchange, false, nil); err != nil {
			return fmt.Errorf("rabbitmq: bind %s → %s: %w", key, sub.Queue, err)
		}
	}
	seen := map[string]bool{}
	for _, d := range cfg.RetryDelays {
		name := RetryQueueName(sub.Queue, d)
		if seen[name] {
			continue
		}
		seen[name] = true
		args := amqp.Table{
			"x-message-ttl":             d.Milliseconds(),
			"x-dead-letter-exchange":    "",
			"x-dead-letter-routing-key": sub.Queue,
		}
		if _, err := ch.QueueDeclare(name, true, false, false, false, args); err != nil {
			return fmt.Errorf("rabbitmq: declare %s: %w", name, err)
		}
	}
	if _, err := ch.QueueDeclare(DLQName(sub.Queue), true, false, false, false, nil); err != nil {
		return fmt.Errorf("rabbitmq: declare %s: %w", DLQName(sub.Queue), err)
	}
	return nil
}

// Consume runs sub with cfg.Workers goroutines until ctx is cancelled or the channel closes.
func (c *Connection) Consume(ctx context.Context, sub Subscription, logger *slog.Logger) error {
	ch, err := c.conn.Channel()
	if err != nil {
		return fmt.Errorf("rabbitmq: consumer channel: %w", err)
	}
	defer func() { _ = ch.Close() }()
	if err := DeclareTopology(ch, c.cfg, sub); err != nil {
		return err
	}
	if err := ch.Qos(c.cfg.Prefetch, 0, false); err != nil {
		return fmt.Errorf("rabbitmq: qos: %w", err)
	}
	pub, err := c.NewPublisher()
	if err != nil {
		return err
	}
	defer func() { _ = pub.Close() }()

	deliveries, err := ch.ConsumeWithContext(ctx, sub.Queue, "", false, false, false, false, nil)
	if err != nil {
		return fmt.Errorf("rabbitmq: consume %s: %w", sub.Queue, err)
	}
	log := logger.With("queue", sub.Queue)
	var wg sync.WaitGroup
	for i := 0; i < c.cfg.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for msg := range deliveries {
				c.handle(ctx, pub, sub, msg, log)
			}
		}()
	}
	wg.Wait()
	if ctx.Err() != nil {
		return nil
	}
	return fmt.Errorf("rabbitmq: delivery channel %s tertutup", sub.Queue)
}

// republisher is the part of Publisher that handle needs (memudahkan unit test).
type republisher interface {
	publishRaw(ctx context.Context, exchange, key string, msg amqp.Publishing) error
}

func (c *Connection) handle(ctx context.Context, pub republisher, sub Subscription, msg amqp.Delivery, log *slog.Logger) {
	attempt := AttemptOf(msg.Headers)
	var env Envelope
	herr := json.Unmarshal(msg.Body, &env)
	if herr != nil {
		herr = Permanent(fmt.Errorf("decode envelope: %w", herr))
	} else if verr := env.Validate(); verr != nil {
		herr = Permanent(verr)
	} else {
		hctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		herr = safeCall(hctx, sub.Handler, Delivery{Envelope: env, RoutingKey: msg.RoutingKey, Attempt: attempt})
		cancel()
	}

	dec := Decide(sub.Queue, attempt, herr, c.cfg.MaxRetries, c.cfg.RetryDelays)
	switch dec.Action {
	case ActionAck:
		_ = msg.Ack(false)
		return
	case ActionRetry, ActionDeadLetter:
		headers := amqp.Table{}
		for k, v := range msg.Headers {
			headers[k] = v
		}
		headers[HeaderAttempt] = int32(attempt + 1) //nolint:gosec // attempt dibatasi MaxRetries ≤ 10
		headers[HeaderLastError] = truncate(herr.Error(), 512)
		headers[HeaderOrigQueue] = sub.Queue
		out := amqp.Publishing{
			Headers: headers, ContentType: msg.ContentType, DeliveryMode: amqp.Persistent,
			MessageId: msg.MessageId, CorrelationId: msg.CorrelationId, Type: msg.Type,
			Timestamp: msg.Timestamp, AppId: msg.AppId, Body: msg.Body,
		}
		pctx, cancel := context.WithTimeout(context.Background(), c.cfg.ConfirmWait+time.Second)
		perr := pub.publishRaw(pctx, "", dec.Queue, out)
		cancel()
		if perr != nil {
			// Belum aman dipindah: kembalikan ke queue agar tidak hilang.
			log.Error("republish gagal, requeue", "messageId", msg.MessageId, "target", dec.Queue, "error", perr)
			_ = msg.Nack(false, true)
			return
		}
		_ = msg.Ack(false)
		level := slog.LevelWarn
		if dec.Action == ActionDeadLetter {
			level = slog.LevelError
		}
		log.Log(ctx, level, "pesan gagal diproses", "messageId", msg.MessageId, "eventType", msg.Type,
			"attempt", attempt, "target", dec.Queue, "permanent", IsPermanent(herr), "error", herr)
	}
}

// AttemptOf reads the attempt number from headers (1 when absent).
func AttemptOf(h amqp.Table) int {
	switch v := h[HeaderAttempt].(type) {
	case int32:
		return int(v)
	case int64:
		return int(v)
	case int:
		return v
	default:
		return 1
	}
}

func safeCall(ctx context.Context, h Handler, d Delivery) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = Permanent(fmt.Errorf("handler panic: %v", p))
		}
	}()
	return h(ctx, d)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// ErrNoSubscriptions is returned when a worker has nothing to consume.
var ErrNoSubscriptions = errors.New("rabbitmq: tidak ada subscription")
