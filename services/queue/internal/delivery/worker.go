package delivery

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/config"
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

	mod, err := newQueueModule(cfg, clients, logger)
	if err != nil {
		return err
	}
	defer func() {
		if mod.rmqConn != nil {
			_ = mod.rmqConn.Close()
		}
	}()
	// Consume Booking events (release slots, block quota, close on sold-out) alongside the jobs.
	if clients.MQ != nil && mod.consumer != nil {
		go func() {
			if cerr := clients.MQ.Consume(ctx, mod.consumer.Subscription(), logger); cerr != nil {
				logger.Error("queue consumer berhenti", "error", cerr)
			}
		}()
	}

	err = loop(ctx, cfg.Worker.Interval, mod.jobs, logger)
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

// jobs builds the worker's background jobs from a composed module (empty without Postgres/Valkey).
func jobs(cfg config.Config, clients *Clients, logger *slog.Logger) []Job {
	mod, err := newQueueModule(cfg, clients, logger)
	if err != nil {
		logger.Error("queue: gagal menyusun worker jobs", "error", err)
		return nil
	}
	if mod.rmqConn != nil {
		_ = mod.rmqConn.Close()
	}
	return mod.jobs
}
