package delivery

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/rabbitmq"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/config"
)

// Job is one periodic background job. Notification has none (pure consumer) but the loop is kept
// for graceful shutdown symmetry with the other services.
type Job struct {
	Name string
	Run  func(context.Context) error
}

// RunWorker connects the needed clients, serves GET /health on APP_PORT, and consumes the three
// event streams (booking.*, queue.turn.*, otp.requested) until ctx is cancelled.
func RunWorker(ctx context.Context, cfg config.Config, logger *slog.Logger, need Need) error {
	clients, err := NewClients(ctx, cfg, need)
	if err != nil {
		return err
	}
	defer clients.Close()

	handler, err := NewHandler(cfg, clients, logger)
	if err != nil {
		return err
	}
	errCh := make(chan error, 1)
	go func() { errCh <- serve(ctx, cfg, handler, logger) }()

	otpConn, cerr := startConsumers(ctx, cfg, clients, logger)
	if otpConn != nil {
		defer func() { _ = otpConn.Close() }()
	}
	if cerr != nil {
		logger.Error("notification: gagal memulai consumer", "error", cerr)
	}

	err = loop(ctx, cfg.Worker.Interval, jobs(clients), logger)
	if serr := <-errCh; serr != nil && !errors.Is(serr, context.Canceled) {
		return serr
	}
	return err
}

func loop(ctx context.Context, every time.Duration, js []Job, logger *slog.Logger) error {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			for _, j := range js {
				if err := j.Run(ctx); err != nil {
					logger.Error("worker job gagal", "job", j.Name, "error", err)
				}
			}
		}
	}
}

// startConsumers subscribes the three consumers when RabbitMQ + Postgres are available. The OTP
// queue runs on a dedicated connection with MaxRetries=0 (time-sensitive, no retry queue). Returns
// the OTP connection so the caller can close it.
func startConsumers(ctx context.Context, cfg config.Config, clients *Clients, logger *slog.Logger) (*rabbitmq.Connection, error) {
	if clients.MQ == nil || clients.PG == nil {
		return nil, nil
	}
	mod, err := newNotificationModule(cfg, clients, logger)
	if err != nil {
		return nil, err
	}

	// booking.* and queue.turn.* use the retrying connection.
	go consume(ctx, clients.MQ, mod.bookingConsumer.Subscription(), logger)
	go consume(ctx, clients.MQ, mod.turnConsumer.Subscription(), logger)

	// otp.requested: dedicated connection, MaxRetries=0 → failures go straight to DLQ (never retried).
	otpCfg := cfg.RabbitMQ
	otpCfg.MaxRetries = 0
	otpCfg.RetryDelays = nil
	otpConn, err := rabbitmq.Dial(otpCfg)
	if err != nil {
		return nil, err
	}
	go consume(ctx, otpConn, mod.otpConsumer.Subscription(), logger)
	return otpConn, nil
}

func consume(ctx context.Context, conn *rabbitmq.Connection, sub rabbitmq.Subscription, logger *slog.Logger) {
	if err := conn.Consume(ctx, sub, logger); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("notification: consumer berhenti", "queue", sub.Queue, "error", err)
	}
}

// jobs returns the periodic background jobs of this worker. Notification is a pure consumer: none.
func jobs(*Clients) []Job { return nil }
