package delivery

import (
	"context"
	"log/slog"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/postgres"

	bookingpg "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/infrastructure/postgres"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/config"
)

// PIIBackfill encrypts legacy plaintext buyer emails (`booking pii-backfill`). Needs PII_ENC_KEY like the service.
func PIIBackfill(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	pii, err := buildPII()
	if err != nil {
		return err
	}
	pg, err := postgres.New(ctx, cfg.Postgres)
	if err != nil {
		return err
	}
	defer pg.Close()
	a, b, err := bookingpg.BackfillBuyerEmail(ctx, pg.Pool(), pii.SealBuyerEmail)
	logger.Info("pii-backfill selesai", "admissions", a, "bookings", b)
	return err
}
