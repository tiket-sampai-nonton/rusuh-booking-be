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
	batch   int
	every   time.Duration
	metrics outbound.IEntryAuditMetrics

	dropped  atomic.Int64
	closed   atomic.Bool
	running  atomic.Bool
	stopOnce sync.Once
}

// NewAsyncEntryAudit returns a writer that stores rows with COPY into entry_audit. buffer is the queue capacity,
// batch the largest COPY, every the flush interval. Call Run once, and Close before the pool is closed.
func NewAsyncEntryAudit(pool *pgxpool.Pool, buffer, batch int, every time.Duration) *AsyncEntryAudit {
	return newAsyncEntryAudit(copyEntryAudit(pool), buffer, batch, every)
}

func newAsyncEntryAudit(insert insertFunc, buffer, batch int, every time.Duration) *AsyncEntryAudit {
	return &AsyncEntryAudit{
		insert:  insert,
		rows:    make(chan outbound.AuditRecord, buffer),
		kick:    make(chan struct{}, 1),
		stop:    make(chan struct{}),
		runDone: make(chan struct{}),
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
	if a.closed.Load() {
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

// Run writes the buffered rows until ctx ends or Close is called. A batch that is already being written is finished
// first, so a cancelled ctx does not cut a COPY in half.
func (a *AsyncEntryAudit) Run(ctx context.Context) {
	a.running.Store(true)
	defer close(a.runDone)
	t := time.NewTicker(a.every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-a.stop:
			return
		case <-t.C:
			a.flush(context.WithoutCancel(ctx), len(a.rows))
		case <-a.kick:
			a.flush(context.WithoutCancel(ctx), len(a.rows))
		}
	}
}

// Close stops Run and writes the rows still buffered. It returns once they are written or ctx ends. Rows left at the
// deadline are dropped and counted, and reported in the error. Rows appended after Close started are dropped too.
// Close must run before the pool is closed.
func (a *AsyncEntryAudit) Close(ctx context.Context) error {
	before := a.dropped.Load()
	a.closed.Store(true)
	a.stopOnce.Do(func() { close(a.stop) })
	if a.running.Load() {
		select {
		case <-a.runDone:
		case <-ctx.Done():
		}
	}
	for len(a.rows) > 0 && ctx.Err() == nil {
		a.flush(ctx, len(a.rows))
	}
	a.discard()
	if lost := a.dropped.Load() - before; lost > 0 {
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

// discard empties the buffer without writing and counts the rows as dropped after shutdown.
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
