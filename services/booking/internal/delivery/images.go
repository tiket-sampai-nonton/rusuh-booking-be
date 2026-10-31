package delivery

import (
	"context"
	"net/http"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/infrastructure/storage"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/config"
)

// mediaRoutes serves locally stored images under /media/ (development only).
type mediaRoutes struct{ h http.Handler }

func (m mediaRoutes) Register(mux *http.ServeMux) { mux.Handle("GET /media/", m.h) }

// newImageStore picks the image store from config; only the local store has routes to serve files.
func newImageStore(ctx context.Context, cfg config.Image) (outbound.IImageStore, registrar, error) {
	if cfg.Storage == "s3" {
		s, err := storage.NewS3(ctx, cfg.S3Bucket, cfg.S3Endpoint, cfg.PublicBaseURL)
		return s, noRoutes{}, err
	}
	l := storage.NewLocal(cfg.LocalDir, cfg.PublicBaseURL)
	return l, mediaRoutes{l.Handler()}, nil
}
