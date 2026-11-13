package postgres

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// fakeInserter records the batch sizes it was asked to write. When block is non-nil, insert waits on it (or on ctx).
type fakeInserter struct {
	mu    sync.Mutex
	block chan struct{}
	sizes []int
	rows  []outbound.AuditRecord
}

func (f *fakeInserter) insert(ctx context.Context, rows []outbound.AuditRecord) error {
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sizes = append(f.sizes, len(rows))
	f.rows = append(f.rows, rows...)
	return nil
}

func (f *fakeInserter) written() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.rows)
}

func (f *fakeInserter) batchSizes() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int(nil), f.sizes...)
}

// fakeDropMetrics records the dropped rows per reason.
type fakeDropMetrics struct {
	mu     sync.Mutex
	counts map[string]int
}

func (m *fakeDropMetrics) Dropped(reason string, rows int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.counts == nil {
		m.counts = map[string]int{}
	}
	m.counts[reason] += rows
}

func (m *fakeDropMetrics) count(reason string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.counts[reason]
}

func auditRow(seq int) outbound.AuditRecord {
	return outbound.AuditRecord{
		EventID: "11111111-1111-1111-1111-111111111111", EntryID: "22222222-2222-2222-2222-222222222222",
		Seq: int64(seq), Action: "JOINED", OccurredAt: time.Unix(1_700_000_000, 0).UTC(),
	}
}

func TestAsyncEntryAudit_BatchesAndDropsWhenFull(t *testing.T) {
	t.Run("flushes in batches of at most 500 rows", func(t *testing.T) {
		ins := &fakeInserter{}
		a := newAsyncEntryAudit(ins.insert, 2000, 500, time.Hour)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go a.Run(ctx)

		for i := range 1200 {
			require.NoError(t, a.Append(ctx, auditRow(i)))
		}
		// The 500-row threshold flushes without waiting for the (one hour) ticker.
		require.Eventually(t, func() bool { return ins.written() >= 500 }, 5*time.Second, 5*time.Millisecond)
		require.NoError(t, a.Close(context.Background()))

		assert.Equal(t, 1200, ins.written(), "every appended row is written by Close")
		for _, n := range ins.batchSizes() {
			assert.LessOrEqual(t, n, 500)
		}
		assert.Zero(t, a.Dropped())
	})

	t.Run("drops rows when the buffer is full and counts them", func(t *testing.T) {
		ins := &fakeInserter{}
		metrics := &fakeDropMetrics{}
		a := newAsyncEntryAudit(ins.insert, 10, 500, time.Hour).WithMetrics(metrics)

		// Run is not started: the buffer stays full, which is the same as a writer that cannot keep up.
		for i := range 25 {
			require.NoError(t, a.Append(context.Background(), auditRow(i)), "Append never fails the request")
		}
		assert.EqualValues(t, 15, a.Dropped())
		assert.Equal(t, 15, metrics.count(outbound.AuditDropBufferFull))

		require.NoError(t, a.Close(context.Background()))
		assert.Equal(t, 10, ins.written(), "the 10 buffered rows are flushed on shutdown")

		require.NoError(t, a.Append(context.Background(), auditRow(99)))
		assert.EqualValues(t, 16, a.Dropped(), "rows after Close are dropped, not queued")
		assert.Equal(t, 1, metrics.count(outbound.AuditDropClosed))
	})

	t.Run("Append never blocks while the writer is stuck", func(t *testing.T) {
		release := make(chan struct{})
		ins := &fakeInserter{block: release}
		a := newAsyncEntryAudit(ins.insert, 10, 5, time.Millisecond)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go a.Run(ctx)
		t.Cleanup(func() { close(release) })

		done := make(chan struct{})
		go func() {
			defer close(done)
			for i := range 1000 {
				_ = a.Append(ctx, auditRow(i))
			}
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("Append blocked on a stuck writer")
		}
		// At most 5 rows are held by the blocked insert and 10 by the buffer, so at least 985 were dropped.
		assert.GreaterOrEqual(t, a.Dropped(), int64(985))
	})
}

func TestAsyncEntryAudit_CloseIsBoundedWhenWriterIsStuck(t *testing.T) {
	release := make(chan struct{})
	ins := &fakeInserter{block: release}
	a := newAsyncEntryAudit(ins.insert, 10, 5, time.Hour)
	runCtx, stopRun := context.WithCancel(context.Background())
	defer stopRun()
	go a.Run(runCtx)
	t.Cleanup(func() { close(release) })

	for i := range 8 {
		require.NoError(t, a.Append(runCtx, auditRow(i)))
	}
	stopRun()

	closeCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	err := a.Close(closeCtx)
	assert.Error(t, err, "rows that could not be written before the deadline are reported")
	assert.Less(t, time.Since(started), 2*time.Second, "shutdown waits at most the given deadline")
	assert.Positive(t, a.Dropped())
}

// hangingInserter is a database that never answers: every COPY waits until its context ends.
type hangingInserter struct {
	startedOnce, abortedOnce sync.Once
	started, aborted         chan struct{}
}

func newHangingInserter() *hangingInserter {
	return &hangingInserter{started: make(chan struct{}), aborted: make(chan struct{})}
}

func (h *hangingInserter) insert(ctx context.Context, _ []outbound.AuditRecord) error {
	h.startedOnce.Do(func() { close(h.started) })
	<-ctx.Done()
	h.abortedOnce.Do(func() { close(h.aborted) })
	return ctx.Err()
}

// A COPY that never returns must not hold shutdown past its deadline, and its context must end so the connection is
// released before the pool closes.
func TestAsyncEntryAudit_CloseAbortsBlockedCopyAtDeadline(t *testing.T) {
	ins := newHangingInserter()
	a := newAsyncEntryAudit(ins.insert, 10, 5, time.Millisecond)
	go a.Run(context.Background())
	for i := range 5 {
		require.NoError(t, a.Append(context.Background(), auditRow(i)))
	}
	select {
	case <-ins.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the writer never started the COPY")
	}

	closeCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	began := time.Now()
	err := a.Close(closeCtx)
	assert.Less(t, time.Since(began), time.Second, "Close returns at its deadline, not when the COPY would finish")
	assert.Error(t, err, "the aborted batch is reported")

	select {
	case <-ins.aborted:
	case <-time.After(5 * time.Second):
		t.Fatal("the blocked COPY never saw its context end")
	}
	assert.Eventually(t, func() bool { return a.Dropped() == 5 }, 5*time.Second, 5*time.Millisecond,
		"the aborted batch is counted once")
}

// A SIGTERM cancels the context passed to Run while the HTTP drain still calls Append. Flushing must go on until
// Close, so rows appended during the drain are not left to fill the buffer.
func TestAsyncEntryAudit_ParentCancelDoesNotStopFlushing(t *testing.T) {
	ins := &fakeInserter{}
	a := newAsyncEntryAudit(ins.insert, 100, 500, 5*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	go a.Run(ctx)
	cancel()

	require.NoError(t, a.Append(context.Background(), auditRow(1)))
	require.Eventually(t, func() bool { return ins.written() == 1 }, 5*time.Second, 5*time.Millisecond,
		"the interval flush still runs after the parent context ended")
	require.NoError(t, a.Close(context.Background()))
	assert.Zero(t, a.Dropped())
}
