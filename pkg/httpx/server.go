package httpx

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// Run serves srv until ctx is cancelled, then shuts down gracefully within shutdownTimeout.
func Run(ctx context.Context, srv *http.Server, shutdownTimeout time.Duration, logger *slog.Logger) error {
	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		return fmt.Errorf("httpx: listen %s: %w", srv.Addr, err)
	}
	return Serve(ctx, srv, ln, shutdownTimeout, logger)
}

// Serve is Run with a caller-provided listener (useful in tests).
func Serve(ctx context.Context, srv *http.Server, ln net.Listener, shutdownTimeout time.Duration, logger *slog.Logger) error {
	errCh := make(chan error, 1)
	go func() {
		logger.Info("http server listening", "addr", ln.Addr().String())
		errCh <- srv.Serve(ln)
	}()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}

	logger.Info("http server shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("httpx: shutdown: %w", err)
	}
	return nil
}

// NewServer returns an http.Server with safe default timeouts.
func NewServer(addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
}
