package delivery

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/outbox"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/config"
)

// Job is one periodic background job (mis. sweeper TTL, outbox relay, rekonsiliasi).
type Job struct {
	Name string
	Run  func(context.Context) error
}

// RunWorker connects the needed clients, serves GET /health on APP_PORT, and runs jobs every
// WORKER_INTERVAL until ctx is cancelled. Job nyata didaftarkan di jobs() saat fitur dibangun.
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

	js, err := jobs(clients, logger)
	if err != nil {
		return err
	}
	err = loop(ctx, cfg.Worker.Interval, js, logger)
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

// jobs returns the background jobs of this worker. Kosong di boilerplate.
// jobs are the periodic tasks of the worker: the outbox relay publishes payment.succeeded and payment.failed.
func jobs(clients *Clients, logger *slog.Logger) ([]Job, error) {
	if clients.PG == nil || clients.MQ == nil {
		return nil, nil
	}
	pub, err := clients.MQ.NewPublisher()
	if err != nil {
		return nil, err
	}
	relay := outbox.NewRelay(clients.PG.Pool(), pub, "payment-service", logger)
	return []Job{{Name: "outbox-relay", Run: func(ctx context.Context) error {
		_, err := relay.RunOnce(ctx)
		return err
	}}}, nil
}
