package usecase

import (
	"strconv"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/apperror"
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
