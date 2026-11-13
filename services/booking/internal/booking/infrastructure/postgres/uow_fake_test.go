package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
)

// fakeTx stands in for pgx.Tx in the unit tests of the unit of work. QueryRow and Query answer from queues in call
// order; Exec is recorded and answers execErr. Any other pgx.Tx method panics (nil embedded interface), so a test
// notices a call it did not expect. The SQL itself is proven by the integration tests.
type fakeTx struct {
	pgx.Tx
	rows    []txRow
	queries []*txRows
	execs   []fakeExec
	execErr error
	// execErrOn limits execErr to statements containing this text ("" = every Exec fails with execErr).
	execErrOn string
	reads     []fakeExec // QueryRow calls, for asserting arguments
}

type fakeExec struct {
	sql  string
	args []any
}

func (f *fakeTx) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	f.reads = append(f.reads, fakeExec{sql: sql, args: args})
	if len(f.rows) == 0 {
		return txRow{err: fmt.Errorf("unexpected QueryRow: %s", sql)}
	}
	r := f.rows[0]
	f.rows = f.rows[1:]
	return r
}

func (f *fakeTx) Query(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
	if len(f.queries) == 0 {
		return nil, fmt.Errorf("unexpected Query: %s", sql)
	}
	r := f.queries[0]
	f.queries = f.queries[1:]
	return r, nil
}

func (f *fakeTx) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	f.execs = append(f.execs, fakeExec{sql: sql, args: args})
	if f.execErrOn != "" && !strings.Contains(sql, f.execErrOn) {
		return pgconn.CommandTag{}, nil
	}
	return pgconn.CommandTag{}, f.execErr
}

// query queues one Query answer.
func (f *fakeTx) query(rows ...[]any) *fakeTx {
	f.queries = append(f.queries, &txRows{data: rows})
	return f
}

// exec returns the first recorded Exec whose SQL contains substr, or nil.
func (f *fakeTx) exec(substr string) *fakeExec {
	for i := range f.execs {
		if strings.Contains(f.execs[i].sql, substr) {
			return &f.execs[i]
		}
	}
	return nil
}

// events lists the appended event types with their stream version, e.g. "BookingPaid@3".
func (f *fakeTx) events() []string {
	var out []string
	for _, e := range f.execs {
		if strings.Contains(e.sql, "INSERT INTO booking_events") {
			out = append(out, fmt.Sprintf("%v@%v", e.args[2], e.args[1]))
		}
	}
	return out
}

// outbox lists the routing keys written to the outbox, in order.
func (f *fakeTx) outbox() []string {
	var out []string
	if e := f.exec("INTO booking.outbox_events"); e != nil {
		for i := 0; i < len(e.args); i += 3 {
			out = append(out, e.args[i].(string))
		}
	}
	return out
}

// outboxPayload decodes the payload of the outbox row with the routing key.
func (f *fakeTx) outboxPayload(t *testing.T, key string) map[string]any {
	t.Helper()
	e := f.exec("INTO booking.outbox_events")
	require.NotNil(t, e, "no outbox write")
	for i := 0; i < len(e.args); i += 3 {
		if e.args[i] == key {
			var m map[string]any
			require.NoError(t, json.Unmarshal(e.args[i+2].([]byte), &m))
			return m
		}
	}
	t.Fatalf("no outbox row %s", key)
	return nil
}

// row queues one QueryRow answer.
func (f *fakeTx) row(vals ...any) *fakeTx {
	f.rows = append(f.rows, txRow{vals: vals})
	return f
}

// rowErr queues one failing QueryRow answer.
func (f *fakeTx) rowErr(err error) *fakeTx {
	f.rows = append(f.rows, txRow{err: err})
	return f
}

// txRow scans vals into the destinations in order, converting to the destination type (e.g. string →
// vo.BookingStatus). A nil value leaves the destination at its zero value.
type txRow struct {
	vals []any
	err  error
}

func (r txRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	return scanInto(r.vals, dest)
}

func scanInto(vals, dest []any) error {
	if len(vals) != len(dest) {
		return fmt.Errorf("fake scan: %d values for %d destinations", len(vals), len(dest))
	}
	for i, v := range vals {
		target := reflect.ValueOf(dest[i]).Elem()
		if v == nil {
			target.Set(reflect.Zero(target.Type()))
			continue
		}
		rv := reflect.ValueOf(v)
		if target.Kind() == reflect.Pointer && rv.Kind() != reflect.Pointer {
			// A nullable column (*int, *time.Time) given as a plain value.
			p := reflect.New(target.Type().Elem())
			p.Elem().Set(rv.Convert(target.Type().Elem()))
			target.Set(p)
			continue
		}
		target.Set(rv.Convert(target.Type()))
	}
	return nil
}

// txRows is a Query answer: one []any per row.
type txRows struct {
	pgx.Rows
	data [][]any
	i    int
	err  error
}

func (r *txRows) Next() bool {
	if r.i >= len(r.data) {
		return false
	}
	r.i++
	return true
}

func (r *txRows) Scan(dest ...any) error { return scanInto(r.data[r.i-1], dest) }
func (r *txRows) Close()                 {}
func (r *txRows) Err() error             { return r.err }

var errDB = errors.New("db down")
