// Package delivery is the composition root: the only place where adapters are wired to usecases.
package delivery

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/httpx"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/telemetry"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/application/usecase"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/infrastructure/healthcheck"
	bookinghttp "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/infrastructure/http"
	bookingpg "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/infrastructure/postgres"
	bookingtelemetry "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/infrastructure/telemetry"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/config"
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
	bookinghttp.NewHealthHandler(health).Register(mux)

	mod, err := newBookingModule(cfg, clients, logger)
	if err != nil {
		return nil, err
	}
	mod.Register(mux)
	routes := mod.WrapVisitor(mux)

	mux.Handle("GET /metrics", telemetry.Handler())
	registerFunnelMetrics(clients)

	// AccessLog dan telemetry.HTTP membaca r.Pattern, jadi harus di dalam RequestID dan langsung membungkus mux.
	trustProxy := cfg.App.Env != "local"
	limit := httpx.RateLimit(cfg.HTTP.RateLimitPerIP, cfg.HTTP.RateLimitWindow, httpx.IPKey(trustProxy, "/health", "/metrics"), time.Now)
	return httpx.Chain(routes, httpx.CORS(), httpx.RequestID(), httpx.AccessLog(logger), telemetry.HTTP(config.ServiceName), httpx.Recover(logger), limit), nil
}

var funnelOnce sync.Once

// registerFunnelMetrics exposes the conversion funnel (NFR-09) on the API's /metrics only: the API is what Prometheus
// and CloudWatch scrape, and one reader per process is enough because the numbers come from PostgreSQL. Once guards
// the process-wide registry against a second NewHandler (tests).
func registerFunnelMetrics(clients *Clients) {
	if clients.PG == nil {
		return
	}
	pool := clients.PG.Pool()
	funnelOnce.Do(func() {
		telemetry.MustRegister(bookingtelemetry.NewFunnelCollector(bookingpg.NewFunnelStats(pool)))
	})
}
