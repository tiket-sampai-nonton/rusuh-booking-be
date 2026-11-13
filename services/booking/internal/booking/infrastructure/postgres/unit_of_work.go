package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/eventstore"
	pkgpg "github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/postgres"

	domevent "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/event"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

// The unit of work is split by command so each file reads top to bottom as one transaction:
//
//	unit_of_work.go  type, constructor, transaction retry, event append
//	uow_hold.go      Hold
//	uow_replace.go   ReplaceItems
//	uow_settle.go    Settle
//	uow_expire.go    Expire
//	uow_payment.go   PaymentLock, ExtendDeadline, StartVerifying
//	uow_stock.go     shared lock and counter statements (supply, category, quota, admission)
//	uow_outbox.go    outbox rows and their payloads
//	uow_errors.go    constraint → domain error mapping

const maxRetries = 3

// UnitOfWork performs the stock/money commands in one transaction each, hiding the fixed lock order
// (supply → category → quota → booking), the event-store append, the read-model projection, and the
// outbox write. It satisfies outbound.IUnitOfWork.
type UnitOfWork struct {
	pool  *pgxpool.Pool
	store *eventstore.Store
}

// NewUnitOfWork constructs the unit of work over the pool and the booking_events store.
func NewUnitOfWork(pool *pgxpool.Pool) (*UnitOfWork, error) {
	st, err := eventstore.New("booking_events")
	if err != nil {
		return nil, err
	}
	return &UnitOfWork{pool: pool, store: st}, nil
}

// inTx runs fn in a retried transaction (see retry) and returns the value of the last attempt.
func inTx[T any](ctx context.Context, u *UnitOfWork, fn func(pgx.Tx) (T, error)) (T, error) {
	var out T
	err := u.retry(ctx, func(tx pgx.Tx) error {
		v, err := fn(tx)
		out = v
		return err
	})
	return out, err
}

// retry runs fn in a transaction, retrying up to maxRetries while retryable reports the error as transient.
func (u *UnitOfWork) retry(ctx context.Context, fn func(pgx.Tx) error) error {
	var err error
	for attempt := 0; attempt < maxRetries; attempt++ {
		err = pkgpg.WithTx(ctx, u.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, fn)
		if err == nil {
			return nil
		}
		if !retryable(err) {
			return err
		}
	}
	return err
}

// retryable reports whether a failed stock transaction may run again: an event-store version conflict,
// a serialization failure (40001), or a deadlock (40P01). Anything else is returned to the caller as is.
func retryable(err error) bool {
	return errors.Is(err, eventstore.ErrConcurrency) ||
		pkgpg.IsCode(err, pkgpg.CodeSerialization) ||
		pkgpg.IsCode(err, pkgpg.CodeDeadlock)
}

// appendEvent appends one schema-v1 event to the booking stream at expectedVersion. A version conflict returns
// eventstore.ErrConcurrency, which retry turns into a fresh attempt.
func (u *UnitOfWork) appendEvent(ctx context.Context, tx pgx.Tx, bookingID string, expectedVersion int, typ string, payload any, correlationID string) error {
	_, err := u.store.Append(ctx, tx, bookingID, expectedVersion, eventstore.NewEvent{
		Type: typ, SchemaVersion: 1, Payload: payload, Metadata: meta(correlationID),
	})
	return err
}

func meta(correlationID string) map[string]string {
	return map[string]string{"correlationId": correlationID, "actor": "booking-service"}
}

func itemSnapshots(lines []outbound.HoldLine) []domevent.ItemSnapshot {
	out := make([]domevent.ItemSnapshot, 0, len(lines))
	for _, l := range lines {
		out = append(out, domevent.ItemSnapshot{CategoryID: l.CategoryID, UnitPriceMinor: l.UnitPriceMinor, AttendeeNIKMasked: l.AttendeeNIKMasked})
	}
	return out
}

// clockOr is the app clock carried in a spec (D-C3.1); a zero Now falls back to the wall clock in UTC so internal
// callers and older tests keep working.
func clockOr(t time.Time) time.Time {
	if t.IsZero() {
		return time.Now().UTC()
	}
	return t
}
