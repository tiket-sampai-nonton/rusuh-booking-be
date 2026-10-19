package delivery

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/httpx"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/config"
)

func serve(ctx context.Context, cfg config.Config, h http.Handler, logger *slog.Logger) error {
	return httpx.Run(ctx, httpx.NewServer(fmt.Sprintf(":%d", cfg.App.Port), h), cfg.HTTP.ShutdownTimeout, logger)
}
