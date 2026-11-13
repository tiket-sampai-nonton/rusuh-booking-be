package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// insertFunc writes one batch of audit rows.
type insertFunc func(ctx context.Context, rows []outbound.AuditRecord) error

// AsyncEntryAudit is the best-effort writer of entry_audit (D-C8.5). Append only queues the row and never blocks: when
// the buffer is full the row is dropped and counted. Run writes the buffered rows every `every`, or as soon as `batch`
// rows wait, with one COPY per batch. Close stops Run and writes what is still buffered, within its context.
type AsyncEntryAudit struct {
	insert  insertFunc
	rows    chan outbound.AuditRecord
	kick    chan struct{} // a full batch is waiting
	stop    chan struct{}
	runDone chan struct{}
	life    context.Context    // context of every flush; abort ends it
	abort   context.CancelFunc // called when the shutdown deadline passes, so a hung COPY is released
	batch   int
	every   time.Duration
	metrics outbound.IEntryAuditMetrics

	mu       sync.RWMutex // Append holds it shared around the closed check and the send; Close takes it exclusively
	closed   bool
	dropped  atomic.Int64
	running  atomic.Bool
	stopOnce sync.Once
}

// NewAsyncEntryAudit returns a writer that stores rows with COPY into entry_audit. buffer is the queue capacity,
// batch the largest COPY, every the flush interval. Call Run once, and Close before the pool is closed.
func NewAsyncEntryAudit(pool *pgxpool.Pool, buffer, batch int, every time.Duration) *AsyncEntryAudit {
	return newAsyncEntryAudit(copyEntryAudit(pool), buffer, batch, every)
}

func newAsyncEntryAudit(insert insertFunc, buffer, batch int, every time.Duration) *AsyncEntryAudit {
	life, abort := context.WithCancel(context.Background())
	return &AsyncEntryAudit{
		insert:  insert,
		rows:    make(chan outbound.AuditRecord, buffer),
		kick:    make(chan struct{}, 1),
		stop:    make(chan struct{}),
		runDone: make(chan struct{}),
		life:    life,
		abort:   abort,
		batch:   batch,
		every:   every,
	}
}

// WithMetrics reports dropped rows to m. Call it before Run starts.
func (a *AsyncEntryAudit) WithMetrics(m outbound.IEntryAuditMetrics) *AsyncEntryAudit {
	a.metrics = m
	return a
}

// Dropped returns how many rows this writer has not stored (buffer full, failed batch, or after shutdown).
func (a *AsyncEntryAudit) Dropped() int64 { return a.dropped.Load() }

// Append queues one row. It never blocks and never fails the caller: a full buffer or a closed writer drops the row
// and counts it. It implements outbound.IEntryAuditRepo.
func (a *AsyncEntryAudit) Append(_ context.Context, e outbound.AuditRecord) error {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.closed {
		a.drop(1, outbound.AuditDropClosed)
		return nil
	}
	select {
	case a.rows <- e:
	default:
		a.drop(1, outbound.AuditDropBufferFull)
		return nil
	}
	if len(a.rows) >= a.batch {
		select {
		case a.kick <- struct{}{}:
		default: // a kick is already pending
		}
	}
	return nil
}

// Run writes the buffered rows every `every` and whenever a full batch waits, until Close. It ignores cancellation of
// ctx on purpose: a SIGTERM cancels the process context while the HTTP drain still appends rows, and those rows must
// still be written. Only Close stops it. Run is started once.
func (a *AsyncEntryAudit) Run(_ context.Context) {
	a.running.Store(true)
	defer close(a.runDone)
	t := time.NewTicker(a.every)
	defer t.Stop()
	for {
		select {
		case <-a.stop:
			return
		case <-t.C:
			a.flush(a.life, len(a.rows))
		case <-a.kick:
			a.flush(a.life, len(a.rows))
		}
	}
}

// Close stops Run and writes the rows still buffered. It returns when they are written, or when ctx ends. At the
// deadline any flush still in progress is aborted, which releases its database connection; that matters because the
// caller closes the pool right after. Rows that were not written are dropped and counted, and the error says so. Rows
// appended after Close starts are dropped too.
func (a *AsyncEntryAudit) Close(ctx context.Context) error {
	before := a.dropped.Load()
	a.mu.Lock()
	a.closed = true
	a.mu.Unlock()
	a.stopOnce.Do(func() { close(a.stop) })

	// A deadline reached at any point, while Run or this Close is flushing, aborts that flush.
	stopAbort := context.AfterFunc(ctx, a.abort)
	defer stopAbort()

	timedOut := false
	if a.running.Load() {
		select {
		case <-a.runDone:
		case <-ctx.Done():
			timedOut = true
			a.abort()
		}
	}
	for len(a.rows) > 0 && ctx.Err() == nil {
		a.flush(a.life, len(a.rows))
	}
	a.discard()
	a.abort()

	lost := a.dropped.Load() - before
	switch {
	case timedOut:
		return fmt.Errorf("entry_audit: shutdown deadline reached: %d rows dropped, an in-flight batch aborted", lost)
	case lost > 0:
		return fmt.Errorf("entry_audit: %d rows not written at shutdown", lost)
	}
	return nil
}

// flush writes up to n buffered rows, batch by batch. A failed batch is dropped and counted, never retried.
func (a *AsyncEntryAudit) flush(ctx context.Context, n int) {
	for n > 0 {
		rows := a.take(min(a.batch, n))
		if len(rows) == 0 {
			return
		}
		n -= len(rows)
		if err := a.insert(ctx, rows); err != nil {
			a.drop(len(rows), outbound.AuditDropWriteFailed)
			slog.Warn("entry_audit: batch not written", "rows", len(rows), "error", err)
		}
	}
}

// take removes up to n rows from the buffer without blocking.
func (a *AsyncEntryAudit) take(n int) []outbound.AuditRecord {
	rows := make([]outbound.AuditRecord, 0, n)
	for len(rows) < n {
		select {
		case r := <-a.rows:
			rows = append(rows, r)
		default:
			return rows
		}
	}
	return rows
}

// discard empties the buffer without writing and counts the rows as dropped after shutdown. Called only after Close
// has set closed under the exclusive lock, so no Append can add a row that is not counted.
func (a *AsyncEntryAudit) discard() {
	n := 0
	for {
		select {
		case <-a.rows:
			n++
		default:
			if n > 0 {
				a.drop(n, outbound.AuditDropClosed)
			}
			return
		}
	}
}

func (a *AsyncEntryAudit) drop(rows int, reason string) {
	a.dropped.Add(int64(rows))
	if a.metrics != nil {
		a.metrics.Dropped(reason, rows)
	}
}

// copyEntryAudit writes a batch with one COPY. Like EntryAudit.Append it writes no buyer_nik_hash (ADR-030).
func copyEntryAudit(pool *pgxpool.Pool) insertFunc {
	columns := []string{"event_id", "entry_id", "seq", "buyer_nik_hash", "action", "occurred_at", "metadata", "account_id"}
	return func(ctx context.Context, rows []outbound.AuditRecord) error {
		values := make([][]any, len(rows))
		for i, e := range rows {
			meta := e.Metadata
			if meta == nil {
				meta = map[string]any{}
			}
			metaJSON, err := json.Marshal(meta)
			if err != nil {
				return fmt.Errorf("entry_audit: marshal metadata: %w", err)
			}
			var accountID any
			if e.AccountID != "" {
				accountID = e.AccountID
			}
			values[i] = []any{e.EventID, e.EntryID, e.Seq, nil, e.Action, e.OccurredAt, metaJSON, accountID}
		}
		if _, err := pool.CopyFrom(ctx, pgx.Identifier{"entry_audit"}, columns, pgx.CopyFromRows(values)); err != nil {
			return fmt.Errorf("entry_audit: copy: %w", err)
		}
		return nil
	}
}
