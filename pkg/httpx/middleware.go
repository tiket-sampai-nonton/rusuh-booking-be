package httpx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"

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

// CORS adds permissive CORS headers for local development (APP_ENV=local).
// In production this is handled by CloudFront/ALB — never enable in prod.
func CORS() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Visitor-Token, X-Visitor-ID, X-Queue-Token, X-Client-Type, Idempotency-Key, X-Request-ID")
			w.Header().Set("Access-Control-Expose-Headers", "X-Request-ID, Set-Cookie")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(b)
}
