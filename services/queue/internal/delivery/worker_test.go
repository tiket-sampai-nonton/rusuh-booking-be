package delivery

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// syncBuf is a goroutine-safe log sink.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

// startJobs runs runJobs in the background and returns a stop func that cancels and waits for the result.
func startJobs(t *testing.T, js []Job, fallback time.Duration, logger *slog.Logger) (stop func() error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runJobs(ctx, js, fallback, logger) }()
	return func() error {
		cancel()
		select {
		case err := <-done:
			return err
		case <-time.After(2 * time.Second):
			t.Fatal("runJobs did not return after cancel")
			return nil
		}
	}
}

// TC-QUE-30 P1: each job ticks on its own interval.
func TestRunJobs_OwnCadence(t *testing.T) {
	var fast, slow atomic.Int32
	js := []Job{
		{Name: "fast", Every: 5 * time.Millisecond, Run: func(context.Context) error { fast.Add(1); return nil }},
		{Name: "slow", Every: 50 * time.Millisecond, Run: func(context.Context) error { slow.Add(1); return nil }},
	}
	stop := startJobs(t, js, time.Hour, discardLogger())
	time.Sleep(300 * time.Millisecond)
	require.NoError(t, stop())
	assert.GreaterOrEqual(t, int(fast.Load()), 20)
	assert.GreaterOrEqual(t, int(slow.Load()), 3)
	assert.LessOrEqual(t, int(slow.Load()), 8)
}

// TC-QUE-30 P2: a blocked job does not slow another job (invariant #6).
func TestRunJobs_BlockedJobDoesNotStarveOthers(t *testing.T) {
	release := make(chan struct{})
	var other atomic.Int32
	js := []Job{
		{Name: "blocked", Every: 5 * time.Millisecond, Run: func(ctx context.Context) error {
			select {
			case <-release:
			case <-ctx.Done():
			}
			return nil
		}},
		{Name: "other", Every: 5 * time.Millisecond, Run: func(context.Context) error { other.Add(1); return nil }},
	}
	stop := startJobs(t, js, time.Hour, discardLogger())
	time.Sleep(100 * time.Millisecond)
	assert.GreaterOrEqual(t, int(other.Load()), 5)
	close(release)
	require.NoError(t, stop())
}

// TC-QUE-30 P3: a job never overlaps itself and skipped ticks are not queued.
func TestRunJobs_NoOverlapNoBurst(t *testing.T) {
	const every = 10 * time.Millisecond
	var inFlight, maxInFlight, runs atomic.Int32
	js := []Job{{Name: "slowrun", Every: every, Run: func(context.Context) error {
		n := inFlight.Add(1)
		for {
			m := maxInFlight.Load()
			if n <= m || maxInFlight.CompareAndSwap(m, n) {
				break
			}
		}
		runs.Add(1)
		time.Sleep(3 * every)
		inFlight.Add(-1)
		return nil
	}}}
	window := 300 * time.Millisecond
	stop := startJobs(t, js, time.Hour, discardLogger())
	time.Sleep(window)
	require.NoError(t, stop())
	assert.Equal(t, int32(1), maxInFlight.Load())
	// Each run takes ~3 intervals; queued ticks would make it ~window/every (30).
	assert.LessOrEqual(t, int(runs.Load()), int(window/(3*every))+2)
}

// TC-QUE-30 P4: runJobs returns only after every job goroutine ended.
func TestRunJobs_WaitsForAllJobs(t *testing.T) {
	var stopped atomic.Int32
	mk := func(name string) Job {
		return Job{Name: name, Every: time.Millisecond, Run: func(ctx context.Context) error {
			<-ctx.Done()
			time.Sleep(20 * time.Millisecond)
			stopped.Add(1)
			return nil
		}}
	}
	js := []Job{mk("a"), mk("b"), mk("c")}
	stop := startJobs(t, js, time.Hour, discardLogger())
	time.Sleep(30 * time.Millisecond)
	require.NoError(t, stop())
	assert.Equal(t, int32(3), stopped.Load(), "runJobs must not return before jobs stopped")
}

// TC-QUE-30 P5: Every <= 0 falls back; a non-positive fallback is an error and starts nothing.
func TestRunJobs_Fallback(t *testing.T) {
	var n atomic.Int32
	js := []Job{{Name: "nofreq", Every: 0, Run: func(context.Context) error { n.Add(1); return nil }}}
	stop := startJobs(t, js, 10*time.Millisecond, discardLogger())
	time.Sleep(120 * time.Millisecond)
	require.NoError(t, stop())
	assert.GreaterOrEqual(t, int(n.Load()), 3)

	var started atomic.Int32
	bad := []Job{
		{Name: "ok", Every: time.Millisecond, Run: func(context.Context) error { started.Add(1); return nil }},
		{Name: "nofreq", Every: -1, Run: func(context.Context) error { started.Add(1); return nil }},
	}
	err := runJobs(context.Background(), bad, 0, discardLogger())
	require.Error(t, err)
	assert.ErrorContains(t, err, "nofreq")
	time.Sleep(20 * time.Millisecond)
	assert.Zero(t, started.Load())
}

// TC-QUE-30 P6: an error or a panic in one tick is logged and later ticks and other jobs continue.
func TestRunJobs_ErrorAndPanicIsolated(t *testing.T) {
	var failRuns, panicRuns, okRuns atomic.Int32
	js := []Job{
		{Name: "fail", Every: 5 * time.Millisecond, Run: func(context.Context) error { failRuns.Add(1); return errors.New("boom") }},
		{Name: "panic", Every: 5 * time.Millisecond, Run: func(context.Context) error { panicRuns.Add(1); panic("kaboom") }},
		{Name: "ok", Every: 5 * time.Millisecond, Run: func(context.Context) error { okRuns.Add(1); return nil }},
	}
	buf := &syncBuf{}
	stop := startJobs(t, js, time.Hour, slog.New(slog.NewTextHandler(buf, nil)))
	assert.Eventually(t, func() bool {
		return failRuns.Load() >= 3 && panicRuns.Load() >= 3 && okRuns.Load() >= 3
	}, time.Second, 5*time.Millisecond)
	require.NoError(t, stop())
	out := buf.String()
	assert.Contains(t, out, "worker job gagal")
	assert.Contains(t, out, "boom")
	assert.Contains(t, out, "worker job panic")
	assert.Contains(t, out, "job=panic")
}

func TestJobs_EmptyWithoutClients(t *testing.T) {
	assert.Empty(t, jobs(&queueModule{}))
}
