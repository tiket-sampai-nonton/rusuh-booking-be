//go:build integration

package postgres

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	inbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

// supplyWindowTracer counts the statements a transaction runs while it holds the event_supply row lock: the lock
// statement itself is not counted, every later statement is, until the next supply lock or the end of the call.
type supplyWindowTracer struct {
	mu    sync.Mutex
	armed bool
	sqls  []string
}

func (t *supplyWindowTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	t.mu.Lock()
	defer t.mu.Unlock()
	if strings.Contains(data.SQL, "event_supply") && strings.Contains(data.SQL, "FOR UPDATE") {
		t.armed, t.sqls = true, nil
		return ctx
	}
	if t.armed {
		t.sqls = append(t.sqls, strings.Join(strings.Fields(data.SQL), " "))
	}
	return ctx
}

func (t *supplyWindowTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func (t *supplyWindowTracer) statements() ([]string, int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.sqls...), len(t.sqls)
}

// tracedUnitOfWork is a unit of work over a second pool of the same database that reports every statement.
func tracedUnitOfWork(t *testing.T, f fixture) (*UnitOfWork, *supplyWindowTracer) {
	t.Helper()
	tracer := &supplyWindowTracer{}
	cfg := f.pool.Config().Copy()
	cfg.ConnConfig.Tracer = tracer
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	uow, err := NewUnitOfWork(pool)
	require.NoError(t, err)
	return uow, tracer
}

// C7.1 (AC-C7.1 PF-P0-4): hold, settle and expire run few statements while the supply row is locked, so the
// lock is held for less time. The hold case uses edit-disabled lines, the heavier path (two events, locked admission).
// Limits: hold <= 8, settle (PAID) <= 9, expire <= 8.
func TestUnitOfWork_StatementsAfterSupplyLock(t *testing.T) {
	ctx := context.Background()

	t.Run("hold", func(t *testing.T) {
		f := seed(t)
		uow, tracer := tracedUnitOfWork(t, f)
		spec := f.holdSpec(f.q(t, `SELECT gen_random_uuid()::text`))
		spec.CfgEditEnabled = false
		_, err := uow.Hold(ctx, spec)
		require.NoError(t, err)
		sqls, n := tracer.statements()
		t.Logf("statements after the supply lock: %d", n)
		assert.LessOrEqual(t, n, 8, "statements after the supply lock: %d\n%s", n, strings.Join(sqls, "\n"))
	})

	t.Run("settle", func(t *testing.T) {
		f := seed(t)
		uow, tracer := tracedUnitOfWork(t, f)
		spec := f.holdSpec(f.q(t, `SELECT gen_random_uuid()::text`))
		spec.CfgEditEnabled = false
		_, err := f.uow.Hold(ctx, spec)
		require.NoError(t, err)
		settled, err := uow.Settle(ctx, settleSpec(spec.BookingID))
		require.NoError(t, err)
		require.True(t, settled)
		sqls, n := tracer.statements()
		t.Logf("statements after the supply lock: %d", n)
		assert.LessOrEqual(t, n, 9, "statements after the supply lock: %d\n%s", n, strings.Join(sqls, "\n"))
	})

	t.Run("expire", func(t *testing.T) {
		f := seed(t)
		uow, tracer := tracedUnitOfWork(t, f)
		spec := f.holdSpec(f.q(t, `SELECT gen_random_uuid()::text`))
		spec.CfgEditEnabled = false
		_, err := f.uow.Hold(ctx, spec)
		require.NoError(t, err)
		acted, err := uow.Expire(ctx, outbound.ExpireSpec{BookingID: spec.BookingID, Reason: "PAYMENT_TIMEOUT", CorrelationID: spec.BookingID})
		require.NoError(t, err)
		require.True(t, acted)
		sqls, n := tracer.statements()
		t.Logf("statements after the supply lock: %d", n)
		assert.LessOrEqual(t, n, 8, "statements after the supply lock: %d\n%s", n, strings.Join(sqls, "\n"))
	})
}

// B10 / P2-9: the usecase and the admission lock see the event as open; a cancel commits while the hold waits for the
// supply lock. The re-check after the supply lock refuses the hold, and no stock moves.
func TestHold_CancelCommittedWhileWaitingForSupplyIsRefused(t *testing.T) {
	f := seed(t)
	ctx := context.Background()
	before := f.stock(t)

	cancel, err := f.pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = cancel.Rollback(ctx) }()
	_, err = cancel.Exec(ctx, `SELECT 1 FROM booking.event_supply WHERE event_id=$1::uuid FOR UPDATE`, f.eventID)
	require.NoError(t, err)
	_, err = cancel.Exec(ctx, `UPDATE booking.events SET status='CANCELLED' WHERE id=$1::uuid`, f.eventID)
	require.NoError(t, err)

	id := f.q(t, `SELECT gen_random_uuid()::text`)
	done := make(chan error, 1)
	go func() {
		_, err := f.uow.Hold(ctx, f.holdSpec(id))
		done <- err
	}()
	require.Eventually(t, func() bool {
		var waiting int
		_ = f.pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE wait_event_type = 'Lock' AND query LIKE '%event_supply%'`).Scan(&waiting)
		return waiting > 0
	}, 10*time.Second, 20*time.Millisecond, "hold waits for the supply lock")
	require.NoError(t, cancel.Commit(ctx))

	err = <-done
	assert.ErrorIs(t, err, inbound.ErrEventCancelled)
	assert.Equal(t, before, f.stock(t), "stok tidak berubah ketika event dibatalkan selagi hold menunggu")
	assert.Equal(t, 0, f.n(t, `SELECT count(*) FROM booking.bookings WHERE id=$1::uuid`, id))
}
