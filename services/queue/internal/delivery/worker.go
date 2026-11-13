package delivery

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/config"
)

// Job is one periodic background job (mis. sweeper TTL, outbox relay, rekonsiliasi). Every is its own cadence;
// Every <= 0 means "use the runner's fallback interval" (WORKER_INTERVAL).
type Job struct {
	Name  string
	Every time.Duration
	Run   func(context.Context) error
}

// RunWorker connects the needed clients, serves GET /health on APP_PORT, and runs jobs every
// their own interval (WORKER_INTERVAL as fallback) until ctx is cancelled. Job nyata didaftarkan di jobs() saat fitur dibangun.
func RunWorker(ctx context.Context, cfg config.Config, logger *slog.Logger, need Need) error {
	clients, err := NewClients(ctx, cfg, need)
	if err != nil {
		return err
	}

	mod, err := newQueueModule(cfg, clients, logger)
	if err != nil {
		clients.Close()
		return err
	}
	// One module per process. Registered before clients.Close so it runs after the audit flush; runJobs and the
	// server have already returned by then.
	defer func() { _ = mod.Close() }()
	defer clients.Close()

	handler, err := NewHandler(cfg, mod, logger)
	if err != nil {
		return err
	}
	errCh := make(chan error, 1)
	go func() { errCh <- serve(ctx, cfg, handler, logger) }()

	// Consume Booking events (release slots, block quota, close on sold-out) alongside the jobs.
	if clients.MQ != nil && mod.consumer != nil {
		go func() {
			if cerr := clients.MQ.Consume(ctx, mod.consumer.Subscription(), logger); cerr != nil {
				logger.Error("queue consumer berhenti", "error", cerr)
			}
		}()
	}

	err = runJobs(ctx, mod.jobs, cfg.Worker.Interval, logger)
	if serr := <-errCh; serr != nil && !errors.Is(serr, context.Canceled) {
		return serr
	}
	return err
}

// runJobs runs every job on its own ticker in its own goroutine until ctx is cancelled and returns only after all of
// them stopped. A job with Every <= 0 uses fallback; a non-positive fallback is an error and nothing is started.
// A job never overlaps itself (one goroutine, synchronous Run) and ticks missed while it ran are dropped, not queued.
// An error or panic in one job is logged and neither stops it nor any other job (invariant #6). A job that ignores
// ctx delays the return, as before.
func runJobs(ctx context.Context, js []Job, fallback time.Duration, logger *slog.Logger) error {
	every := make([]time.Duration, len(js))
	for i, j := range js {
		every[i] = j.Every
		if every[i] <= 0 {
			every[i] = fallback
		}
		if every[i] <= 0 {
			return fmt.Errorf("worker job %q: interval harus > 0", j.Name)
		}
	}
	var wg sync.WaitGroup
	for i, j := range js {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runJob(ctx, j, every[i], logger)
		}()
	}
	wg.Wait()
	return nil
}

func runJob(ctx context.Context, j Job, every time.Duration, logger *slog.Logger) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			runOnce(ctx, j, logger)
			// A ticker keeps one pending tick while the receiver is busy; drop it so a slow run is not
			// followed by an immediate back-to-back run.
			select {
			case <-t.C:
			default:
			}
		}
	}
}

func runOnce(ctx context.Context, j Job, logger *slog.Logger) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("worker job panic", "job", j.Name, "panic", fmt.Sprint(r))
		}
	}()
	if err := j.Run(ctx); err != nil {
		logger.Error("worker job gagal", "job", j.Name, "error", err)
	}
}

// jobs returns the worker's background jobs of the given module (none without Postgres/Valkey).
func jobs(mod *queueModule) []Job {
	return mod.jobs
}
