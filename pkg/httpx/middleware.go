package httpx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
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

// RequestID reuses an incoming X-Request-ID or generates one, and stores it in the context.
func RequestID() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get(HeaderRequestID)
			if id == "" || len(id) > 128 {
				id = newID()
			}
			w.Header().Set(HeaderRequestID, id)
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, id)))
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
					logger.Error("panic recovered", "panic", rec, "requestId", RequestIDFrom(r.Context()))
					WriteError(w, r, fmt.Errorf("panic: %v", rec))
				}
			}()
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
