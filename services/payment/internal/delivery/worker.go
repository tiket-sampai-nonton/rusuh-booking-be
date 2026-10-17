package delivery

import (
	"context"
	"log/slog"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/config"
)

// RunWorker runs background jobs (expiry, outbox relay, reconciliation) and serves /healthz and /readyz.
// Job nyata didaftarkan di jobs() saat fitur dibangun.
func RunWorker(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	errCh := make(chan error, 1)
	go func() { errCh <- RunServer(ctx, cfg, logger) }()

	ticker := time.NewTicker(cfg.WorkerInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return <-errCh
		case err := <-errCh:
			return err
		case <-ticker.C:
			for _, job := range jobs() {
				if err := job.run(ctx); err != nil {
					logger.Error("worker job failed", "job", job.name, "error", err)
				}
			}
			logger.Debug("worker tick", "jobs", len(jobs()))
		}
	}
}

type job struct {
	name string
	run  func(context.Context) error
}

// jobs returns the background jobs of this worker. Kosong di skeleton.
func jobs() []job { return nil }
