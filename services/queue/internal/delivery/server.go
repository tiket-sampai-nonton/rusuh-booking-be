// Package delivery is the composition root: the only place where adapters are wired to usecases.
package delivery

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/httpx"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/telemetry"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/config"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/application/usecase"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/infrastructure/healthcheck"
	queuehttp "github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/infrastructure/http"
)

// RunServer connects the needed clients, wires the HTTP routes, and serves until ctx is cancelled.
func RunServer(ctx context.Context, cfg config.Config, logger *slog.Logger, need Need) error {
	clients, err := NewClients(ctx, cfg, need)
	if err != nil {
		return err
	}
	defer clients.Close()

	handler, err := NewHandler(cfg, clients, logger)
	if err != nil {
		return err
	}
	srv := httpx.NewServer(fmt.Sprintf(":%d", cfg.App.Port), handler)
	return httpx.Run(ctx, srv, cfg.HTTP.ShutdownTimeout, logger)
}

// NewHandler builds the HTTP handler. Setiap domain mendaftarkan rute miliknya (route registrar).
func NewHandler(cfg config.Config, clients *Clients, logger *slog.Logger) (http.Handler, error) {
	health := usecase.NewCheckHealth(config.ServiceName, healthcheck.New(clients.Pingers(), cfg.HTTP.HealthTimeout), clock.System{})

	mux := http.NewServeMux()
	queuehttp.NewHealthHandler(health).Register(mux)

	mod, err := newQueueModule(cfg, clients, logger)
	if err != nil {
		return nil, err
	}
	mod.Register(mux)

	mux.Handle("GET /metrics", telemetry.Handler())

	// AccessLog dan telemetry.HTTP membaca r.Pattern, jadi harus di dalam RequestID dan langsung membungkus mux.
	return httpx.Chain(mux, httpx.CORS(), httpx.RequestID(), httpx.AccessLog(logger), telemetry.HTTP(config.ServiceName), httpx.Recover(logger)), nil
}
