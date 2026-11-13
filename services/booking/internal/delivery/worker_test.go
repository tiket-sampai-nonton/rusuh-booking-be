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

func TestRunJobs_RunsJobsUntilCancelled(t *testing.T) {
	var ok, failed atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	js := []Job{
		{Name: "ok", Run: func(context.Context) error { ok.Add(1); return nil }},
		{Name: "fail", Run: func(context.Context) error { failed.Add(1); return errors.New("boom") }},
	}
	done := make(chan error, 1)
	go func() {
		done <- runJobs(ctx, js, 5*time.Millisecond, slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()
	assert.Eventually(t, func() bool { return ok.Load() >= 2 && failed.Load() >= 2 }, time.Second, 5*time.Millisecond)
	cancel()
	assert.NoError(t, <-done)
	assert.Empty(t, jobs(&Clients{}))
}

// A job that blocks until its context ends does not delay the other jobs' ticks (invariant #6).
func TestRunJobs_SlowJobDoesNotDelayOthers(t *testing.T) {
	var other atomic.Int32
	js := []Job{
		{Name: "slow", Every: 5 * time.Millisecond, Run: func(ctx context.Context) error {
			<-ctx.Done()
			return nil
		}},
		{Name: "other", Every: 5 * time.Millisecond, Run: func(context.Context) error { other.Add(1); return nil }},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runJobs(ctx, js, time.Hour, slog.New(slog.NewTextHandler(io.Discard, nil))) }()
	assert.Eventually(t, func() bool { return other.Load() >= 5 }, time.Second, 5*time.Millisecond, "other job ticks while slow job blocks")
	cancel()
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("runJobs did not return after cancel")
	}
}
