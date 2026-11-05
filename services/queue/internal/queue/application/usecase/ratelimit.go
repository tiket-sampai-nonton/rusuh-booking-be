package usecase

import (
	"context"
	"strconv"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/apperror"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// statusTooManyRequests mirrors net/http.StatusTooManyRequests without importing a transport package
// into the application layer (depguard: usecases stay transport-free).
const statusTooManyRequests = 429

// apperrorRateLimited returns a 429 RATE_LIMITED error carrying a Retry-After hint (seconds) in the
// error details, so the HTTP handler can set the Retry-After header (design §9).
func apperrorRateLimited(window time.Duration) *apperror.Error {
	secs := int(window.Seconds())
	if secs < 1 {
		secs = 1
	}
	return apperror.New(apperror.CodeRateLimited, statusTooManyRequests, "terlalu banyak permintaan").
		WithDetails(map[string]string{"retryAfter": strconv.Itoa(secs)})
}

// visitorLimit is a per-visitor fixed-window cap on a read/write path (me, heartbeat). A nil limiter or a limit <= 0
// disables it. A limiter error is returned (fail-closed, as for join).
type visitorLimit struct {
	limiter outbound.IRateLimiter
	limit   int
	window  time.Duration
}

// allow counts one call of kind ("me" or "hb") for the visitor and returns a 429 error beyond the limit.
func (l visitorLimit) allow(ctx context.Context, kind, eventID, visitorID string) error {
	if l.limiter == nil || l.limit <= 0 || l.window <= 0 {
		return nil
	}
	ok, err := l.limiter.Allow(ctx, kind+":v:"+eventID+":"+visitorID, l.limit, l.window)
	if err != nil {
		return err
	}
	if !ok {
		return apperrorRateLimited(l.window)
	}
	return nil
}
