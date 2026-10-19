package httpx

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRun_ServesUntilCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	srv := NewServer("127.0.0.1:0", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	done := make(chan error, 1)
	go func() { done <- Run(ctx, srv, time.Second, slog.New(slog.NewTextHandler(io.Discard, nil))) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	assert.NoError(t, <-done)
}

func TestRun_ListenError(t *testing.T) {
	err := Run(context.Background(), NewServer("256.0.0.1:99999", http.NotFoundHandler()), time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "listen")
}
