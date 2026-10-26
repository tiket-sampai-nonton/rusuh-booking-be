// Package emailguard wraps any IEmailSender (SMTP/Mailpit or Resend) with a circuit breaker and a
// bulkhead (R9.5, R13.9). A tripped breaker or full bulkhead is a transient error, so the message goes
// to the RabbitMQ retry queue instead of piling up on a failing provider.
package emailguard

import (
	"context"
	"errors"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/rabbitmq"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/resilience"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/telemetry"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/port/outbound"
)

// shortcut: fixed values from SDD-RBT §4.6.2 (timeout 3 s, threshold 5, cooldown 30 s, probe 1, bulkhead 10); move to config when a provider needs different limits.
const (
	callTimeout      = 3 * time.Second
	failureThreshold = 5
	cooldown         = 30 * time.Second
	halfOpenProbes   = 1
	maxConcurrent    = 10
)

// Sender decorates an IEmailSender with the breaker and bulkhead.
type Sender struct {
	next     outbound.IEmailSender
	breaker  *resilience.Breaker
	bulkhead *resilience.Bulkhead
}

// New wraps next. now drives the breaker cooldown (injected for tests).
func New(next outbound.IEmailSender, now func() time.Time) (*Sender, error) {
	b, err := resilience.NewBreaker(resilience.BreakerConfig{
		Name: "email", FailureThreshold: failureThreshold, Cooldown: cooldown, HalfOpenProbes: halfOpenProbes,
		Timeout:       callTimeout,
		IsFailure:     isProviderFailure,
		OnStateChange: telemetry.BreakerObserver(),
	}, now)
	if err != nil {
		return nil, err
	}
	// Wait 0: a full bulkhead rejects immediately so the consumer is never blocked behind slow sends.
	bh, err := resilience.NewBulkhead(maxConcurrent, 0)
	if err != nil {
		return nil, err
	}
	return &Sender{next: next, breaker: b, bulkhead: bh}, nil
}

// isProviderFailure counts only errors that say the provider is unhealthy. Permanent errors (rejected
// address, bad key), a full bulkhead, and cancellation are not provider failures.
func isProviderFailure(err error) bool {
	return err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, resilience.ErrBulkheadFull) && !rabbitmq.IsPermanent(err)
}

// SendEmail sends through the breaker and bulkhead.
func (s *Sender) SendEmail(ctx context.Context, to, subject, body, html, idempotencyKey string) (string, error) {
	var id string
	err := s.breaker.Do(ctx, func(ctx context.Context) error {
		return s.bulkhead.Do(ctx, func(ctx context.Context) error {
			var err error
			id, err = s.next.SendEmail(ctx, to, subject, body, html, idempotencyKey)
			return err
		})
	})
	return id, err
}
