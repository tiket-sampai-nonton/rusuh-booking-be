package rabbitmq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// Connection owns one AMQP connection. Channels are opened per publisher/consumer.
type Connection struct {
	conn *amqp.Connection
	cfg  Config
}

// Dial connects and declares the shared exchange.
func Dial(cfg Config) (*Connection, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	conn, err := amqp.DialConfig(cfg.URL, amqp.Config{Heartbeat: 10 * time.Second, Locale: "en_US"})
	if err != nil {
		return nil, fmt.Errorf("rabbitmq: dial %s: %w", cfg.Redacted(), err)
	}
	c := &Connection{conn: conn, cfg: cfg}
	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("rabbitmq: channel: %w", err)
	}
	defer func() { _ = ch.Close() }()
	if err := ch.ExchangeDeclare(cfg.Exchange, cfg.ExchangeType, true, false, false, false, nil); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("rabbitmq: declare exchange %s: %w", cfg.Exchange, err)
	}
	return c, nil
}

// Name implements the health Pinger contract.
func (c *Connection) Name() string { return "rabbitmq" }

// Ping reports whether the connection is still open.
func (c *Connection) Ping(context.Context) error {
	if c.conn.IsClosed() {
		return errors.New("rabbitmq: connection closed")
	}
	return nil
}

// NotifyClose returns a channel that receives the close reason. Proses worker keluar saat koneksi putus
// dan dijalankan ulang oleh supervisor (Compose restart policy, systemd di ASG).
func (c *Connection) NotifyClose() <-chan *amqp.Error {
	return c.conn.NotifyClose(make(chan *amqp.Error, 1))
}

// Close closes the connection.
func (c *Connection) Close() error { return c.conn.Close() }

// Publisher publishes envelopes with publisher confirm. Aman dipakai bersamaan.
type Publisher struct {
	mu   sync.Mutex
	ch   *amqp.Channel
	cfg  Config
	conf chan amqp.Confirmation
}

// NewPublisher opens a confirm-mode channel.
func (c *Connection) NewPublisher() (*Publisher, error) {
	ch, err := c.conn.Channel()
	if err != nil {
		return nil, fmt.Errorf("rabbitmq: publisher channel: %w", err)
	}
	if err := ch.Confirm(false); err != nil {
		_ = ch.Close()
		return nil, fmt.Errorf("rabbitmq: confirm mode: %w", err)
	}
	return &Publisher{ch: ch, cfg: c.cfg, conf: ch.NotifyPublish(make(chan amqp.Confirmation, 1))}, nil
}

// Publish sends env to the shared exchange with routingKey and waits for the broker confirm.
// Error berarti pesan belum pasti tersimpan; outbox relay mengulang (ADR-010).
func (p *Publisher) Publish(ctx context.Context, routingKey string, env Envelope) error {
	if err := env.Validate(); err != nil {
		return err
	}
	body, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("rabbitmq: marshal envelope: %w", err)
	}
	return p.publishRaw(ctx, p.cfg.Exchange, routingKey, amqp.Publishing{
		ContentType:   "application/json",
		DeliveryMode:  amqp.Persistent,
		MessageId:     env.MessageID,
		CorrelationId: env.CorrelationID,
		Type:          env.EventType,
		Timestamp:     env.OccurredAt,
		AppId:         env.Producer,
		Body:          body,
	})
}

func (p *Publisher) publishRaw(ctx context.Context, exchange, key string, msg amqp.Publishing) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.ch.PublishWithContext(ctx, exchange, key, false, false, msg); err != nil {
		return fmt.Errorf("rabbitmq: publish %s: %w", key, err)
	}
	timer := time.NewTimer(p.cfg.ConfirmWait)
	defer timer.Stop()
	select {
	case c, ok := <-p.conf:
		if !ok {
			return errors.New("rabbitmq: confirm channel closed")
		}
		if !c.Ack {
			return fmt.Errorf("rabbitmq: broker nack untuk %s", key)
		}
		return nil
	case <-timer.C:
		return fmt.Errorf("rabbitmq: confirm timeout %s untuk %s", p.cfg.ConfirmWait, key)
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close closes the publisher channel.
func (p *Publisher) Close() error { return p.ch.Close() }
