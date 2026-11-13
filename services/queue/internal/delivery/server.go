// Package delivery is the composition root: the only place where adapters are wired to usecases.
package delivery

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"

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

	mod, err := newQueueModule(cfg, clients, logger)
	if err != nil {
		clients.Close()
		return err
	}
	// One module per process. Registered before clients.Close so it runs after the audit flush; httpx.Run has
	// already drained the server by then.
	defer func() { _ = mod.Close() }()
	defer clients.Close()

	handler, err := NewHandler(cfg, mod, logger)
	if err != nil {
		return err
	}
	srv := httpx.NewServer(fmt.Sprintf(":%d", cfg.App.Port), handler)
	return httpx.Run(ctx, srv, cfg.HTTP.ShutdownTimeout, logger)
}

// NewHandler builds the HTTP handler around the given module (built once per process by the caller). Setiap domain
// mendaftarkan rute miliknya (route registrar).
func NewHandler(cfg config.Config, mod *queueModule, logger *slog.Logger) (http.Handler, error) {
	health := usecase.NewCheckHealth(config.ServiceName, healthcheck.New(mod.pingers, cfg.HTTP.HealthTimeout), clock.System{})

	mux := http.NewServeMux()
	queuehttp.NewHealthHandler(health).Register(mux)

	mod.Register(mux)

	mux.Handle("GET /metrics", telemetry.Handler())
	if mod.depth != nil {
		depthOnce.Do(func() { telemetry.MustRegister(mod.depth) })
	}

	// AccessLog dan telemetry.HTTP membaca r.Pattern, jadi harus di dalam RequestID dan langsung membungkus mux.
	return httpx.Chain(mux, httpx.CORS(), httpx.RequestID(), httpx.AccessLog(logger), telemetry.HTTP(config.ServiceName), httpx.Recover(logger)), nil
}

// depthOnce guards the process-wide registry against a second NewHandler (tests); the API's /metrics is what
// Prometheus and CloudWatch scrape, so the worker does not register it.
var depthOnce sync.Once
