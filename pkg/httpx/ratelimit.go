package httpx

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/apperror"
)

// maxLimiterKeys bounds the memory of one limiter; past it, expired windows are dropped first and
// new keys are let through if the table is still full (fail open rather than reject real buyers).
const maxLimiterKeys = 100_000

var errRateLimited = apperror.New(apperror.CodeRateLimited, http.StatusTooManyRequests, "terlalu banyak permintaan; coba lagi nanti")

type window struct {
	count int
	reset time.Time
}

// RateLimit is a fixed-window, in-memory limiter: at most limit requests per window for each key.
// A key function returning "" exempts the request. limit <= 0 disables the limiter.
// shortcut: counters live in this process only (N instances = N x limit), move to Valkey when a hard global cap is needed.
func RateLimit(limit int, per time.Duration, key func(*http.Request) string, now func() time.Time) Middleware {
	if limit <= 0 {
		return func(next http.Handler) http.Handler { return next }
	}
	var (
		mu      sync.Mutex
		windows = map[string]*window{}
	)
	// allow reports whether the request fits and, if not, how long until the window resets.
	allow := func(k string) (bool, time.Duration) {
		t := now()
		mu.Lock()
		defer mu.Unlock()
		w, ok := windows[k]
		if ok && t.Before(w.reset) {
			if w.count >= limit {
				return false, w.reset.Sub(t)
			}
			w.count++
			return true, 0
		}
		if !ok && len(windows) >= maxLimiterKeys {
			for old, ow := range windows {
				if !t.Before(ow.reset) {
					delete(windows, old)
				}
			}
			if len(windows) >= maxLimiterKeys {
				return true, 0
			}
		}
		windows[k] = &window{count: 1, reset: t.Add(per)}
		return true, 0
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			k := key(r)
			if k == "" {
				next.ServeHTTP(w, r)
				return
			}
			if ok, wait := allow(k); !ok {
				secs := int(wait/time.Second) + 1
				w.Header().Set("Retry-After", strconv.Itoa(secs))
				WriteError(w, r, errRateLimited)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// IPKey keys requests by client IP, skipping the listed exact paths (health, metrics, ...).
func IPKey(trustProxy bool, skip ...string) func(*http.Request) string {
	return func(r *http.Request) string {
		for _, p := range skip {
			if r.URL.Path == p {
				return ""
			}
		}
		return ClientIP(r, trustProxy)
	}
}

// OnlyPathKey keys by client IP for one exact path and exempts everything else.
func OnlyPathKey(path string, trustProxy bool) func(*http.Request) string {
	return func(r *http.Request) string {
		if r.URL.Path != path {
			return ""
		}
		return ClientIP(r, trustProxy)
	}
}
