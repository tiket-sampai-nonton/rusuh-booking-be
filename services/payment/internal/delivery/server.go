// Package delivery is the composition root: the only place where adapters are wired to usecases.
package delivery

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/httpx"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/config"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/application/usecase"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/valueobject"
	paymenthttp "github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/infrastructure/http"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/infrastructure/tcpcheck"
)

// RunServer wires dependencies and serves HTTP until ctx is cancelled.
func RunServer(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	handler, err := newHTTPHandler(cfg, logger)
	if err != nil {
		return err
	}
	srv := httpx.NewServer(fmt.Sprintf(":%d", cfg.Port), handler)
	return httpx.Run(ctx, srv, cfg.ShutdownTimeout, logger)
}

func newHTTPHandler(cfg config.Config, logger *slog.Logger) (http.Handler, error) {
	targets := make([]tcpcheck.Target, 0, len(cfg.Dependencies))
	for _, d := range cfg.Dependencies {
		name, err := valueobject.NewDependencyName(d.Name)
		if err != nil {
			return nil, fmt.Errorf("delivery: dependency %q: %w", d.Name, err)
		}
		targets = append(targets, tcpcheck.Target{Name: name, Addr: d.Addr})
	}

	readiness := usecase.NewCheckReadiness(cfg.ServiceName, tcpcheck.New(targets, cfg.DialTimeout), clock.System{})
	h := paymenthttp.NewHandler(cfg.ServiceName, readiness)

	return httpx.Chain(h.Routes(), httpx.RequestID(), httpx.Recover(logger)), nil
}
