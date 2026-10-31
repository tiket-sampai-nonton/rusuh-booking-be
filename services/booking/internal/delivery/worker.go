package delivery

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"

	bookingrabbitmq "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/infrastructure/rabbitmq"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/config"
)

// Job is one periodic background job (mis. sweeper TTL, outbox relay, rekonsiliasi).
type Job struct {
	Name string
	Run  func(context.Context) error
}

// RunWorker connects the needed clients, serves GET /health on APP_PORT, runs the background jobs every
// WORKER_INTERVAL, and consumes payment.* events until ctx is cancelled.
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

	js, consumerErr := startConsumer(ctx, cfg, clients, logger)
	if consumerErr != nil {
		logger.Error("booking: gagal memulai consumer payment.*", "error", consumerErr)
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

// startConsumer subscribes the payment.* consumer (if RabbitMQ + Postgres are available) and returns
// the periodic jobs: the two sweepers and the outbox relay.
func startConsumer(ctx context.Context, cfg config.Config, clients *Clients, logger *slog.Logger) ([]Job, error) {
	mod, err := newBookingModule(cfg, clients, logger)
	if err != nil {
		return nil, err
	}
	var js []Job
	if mod.sweepSess != nil {
		js = append(js, Job{Name: "session-sweeper", Run: mod.sweepSess.SweepSessions})
		js = append(js, Job{Name: "expiry-sweeper", Run: mod.sweepExp.SweepExpiries})
	}
	if mod.projAvail != nil {
		js = append(js, Job{Name: "availability-projector", Run: mod.projAvail.ProjectAvailability})
	}

	if clients.MQ == nil || clients.PG == nil {
		return js, nil
	}
	pub, err := clients.MQ.NewPublisher()
	if err != nil {
		return js, err
	}
	relay := bookingrabbitmq.NewOutboxRelay(mod.outbox, pub, clock.System{}.Now, logger)
	js = append(js, Job{Name: "outbox-relay", Run: relay.RunOnce})

	consumer := bookingrabbitmq.NewPaymentConsumer(mod.settle, logger)
	go func() {
		if cerr := clients.MQ.Consume(ctx, consumer.Subscription(), logger); cerr != nil && !errors.Is(cerr, context.Canceled) {
			logger.Error("booking: consumer payment.* berhenti", "error", cerr)
		}
	}()
	return js, nil
}

// jobs returns the periodic background jobs for a worker with the given clients. Without Postgres it
// returns nil (nothing to sweep or relay). Real wiring happens in startConsumer, which also subscribes
// the payment consumer; this function exists so the composition root (and tests) can inspect the job
// set without opening a broker consumer.
func jobs(clients *Clients) []Job {
	if clients == nil || clients.PG == nil {
		return nil
	}
	mod, err := newBookingModule(config.Config{}, clients, slog.Default())
	if err != nil || mod.sweepSess == nil {
		return nil
	}
	js := []Job{
		{Name: "session-sweeper", Run: mod.sweepSess.SweepSessions},
		{Name: "expiry-sweeper", Run: mod.sweepExp.SweepExpiries},
	}
	if mod.projAvail != nil {
		js = append(js, Job{Name: "availability-projector", Run: mod.projAvail.ProjectAvailability})
	}
	return js
}
