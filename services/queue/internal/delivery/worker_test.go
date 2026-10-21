package delivery

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestLoop_RunsJobsUntilCancelled(t *testing.T) {
	var ok, failed atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	js := []Job{
		{Name: "ok", Run: func(context.Context) error { ok.Add(1); return nil }},
		{Name: "fail", Run: func(context.Context) error { failed.Add(1); return errors.New("boom") }},
	}
	done := make(chan error, 1)
	go func() { done <- loop(ctx, 5*time.Millisecond, js, slog.New(slog.NewTextHandler(io.Discard, nil))) }()
	assert.Eventually(t, func() bool { return ok.Load() >= 2 && failed.Load() >= 2 }, time.Second, 5*time.Millisecond)
	cancel()
	assert.NoError(t, <-done)
	assert.Empty(t, jobs(testConfig(t), &Clients{}, slog.New(slog.NewTextHandler(io.Discard, nil))))
}
