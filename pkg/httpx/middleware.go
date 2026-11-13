package httpx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/logger"
)

// HeaderRequestID is propagated across services and returned to clients.
const HeaderRequestID = "X-Request-ID"

type ctxKey struct{}

// Middleware wraps an http.Handler.
type Middleware func(http.Handler) http.Handler

// Chain applies middlewares so the first one is the outermost.
func Chain(h http.Handler, mws ...Middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

// requestIDRe membatasi X-Request-ID dari klien (ditulis ke log dan diteruskan ke service lain).
var requestIDRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

// RequestID reuses an incoming X-Request-ID or generates one, and stores it (with the trace id) in the context
// and in the logging context, so records logged with that context carry requestId and trace_id.
// Middleware yang membaca r.Pattern (AccessLog, telemetry) harus dipasang di dalam RequestID.
func RequestID() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get(HeaderRequestID)
			if !requestIDRe.MatchString(id) {
				id = newID()
			}
			w.Header().Set(HeaderRequestID, id)
			tr := traceFromHeaders(r.Header)
			ctx := context.WithValue(r.Context(), ctxKey{}, id)
			ctx = context.WithValue(ctx, traceKey{}, tr)
			ctx = logger.ContextWith(ctx, "requestId", id, "trace_id", tr.id)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequestIDFrom returns the request ID stored by the RequestID middleware.
func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(ctxKey{}).(string)
	return id
}

// Recover turns panics into a 500 error envelope.
func Recover(logger *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					logger.ErrorContext(r.Context(), "panic recovered", "panic", rec)
					WriteError(w, r, fmt.Errorf("panic: %v", rec))
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// CORS answers cross-origin browser calls. CORS_ALLOWED_ORIGINS (comma separated, exact match) is an allowlist: a
// matching Origin is echoed back with credentials enabled (cookie rb_visitor needs it), any other Origin gets no CORS
// headers. shortcut: empty list keeps the old wildcard (no credentials) so local dev works unconfigured; CheckCORS makes
// a deployed service (APP_ENV not local) refuse to start without the list. Never put localhost in prod.
func CORS() Middleware {
	allowed := corsOrigins()
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			switch origin := r.Header.Get("Origin"); {
			case len(allowed) == 0:
				h.Set("Access-Control-Allow-Origin", "*")
			case slices.Contains(allowed, origin):
				h.Set("Access-Control-Allow-Origin", origin)
				h.Set("Access-Control-Allow-Credentials", "true")
				h.Add("Vary", "Origin")
			default:
				h.Add("Vary", "Origin")
			}
			h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			h.Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Visitor-Token, X-Visitor-ID, X-Queue-Token, X-Client-Type, Idempotency-Key, X-Request-ID")
			h.Set("Access-Control-Expose-Headers", "X-Request-ID, Set-Cookie")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// isLocalEnv reports whether APP_ENV marks unconfigured local development (the services default to local).
func isLocalEnv() bool {
	e := strings.ToLower(strings.TrimSpace(os.Getenv("APP_ENV")))
	return e == "" || e == "local"
}

// corsOrigins is the CORS_ALLOWED_ORIGINS allowlist with blanks dropped.
func corsOrigins() []string {
	var out []string
	for _, o := range strings.Split(os.Getenv("CORS_ALLOWED_ORIGINS"), ",") {
		if o = strings.TrimSpace(o); o != "" {
			out = append(out, o)
		}
	}
	return out
}

// CheckCORS makes a deployed service refuse to start without a CORS allowlist (SEC-004): outside local an empty
// CORS_ALLOWED_ORIGINS would silently fall back to the wildcard. Call it where the HTTP handler is built.
func CheckCORS() error {
	if isLocalEnv() || len(corsOrigins()) > 0 {
		return nil
	}
	return errors.New("httpx: CORS_ALLOWED_ORIGINS wajib diisi bila APP_ENV bukan local (kosong berarti wildcard)")
}

// SecurityHeaders adds the standard response security headers (SEC-003): nosniff, no Referer, and HSTS outside local
// (plain-HTTP local development must not pin HTTPS). Put it outermost so every answer, preflights included, carries them.
func SecurityHeaders() Middleware {
	hsts := !isLocalEnv()
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("Referrer-Policy", "no-referrer")
			if hsts {
				h.Set("Strict-Transport-Security", "max-age=31536000")
			}
			next.ServeHTTP(w, r)
		})
	}
}

// CookieSameSite is the SameSite mode for session cookies: Lax, or None when COOKIE_SAMESITE=none (the FE runs on
// another site than the API, e.g. localhost or pages.dev). None drops the cookie CSRF barrier, so CORS_ALLOWED_ORIGINS
// must be set when it is used.
func CookieSameSite() http.SameSite {
	if strings.EqualFold(os.Getenv("COOKIE_SAMESITE"), "none") {
		return http.SameSiteNoneMode
	}
	return http.SameSiteLaxMode
}

func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(b)
}
