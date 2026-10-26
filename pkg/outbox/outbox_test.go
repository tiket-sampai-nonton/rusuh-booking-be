package outbox

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeExec struct {
	sql  string
	args []any
	err  error
}

func (f *fakeExec) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	f.sql, f.args = sql, args
	return pgconn.CommandTag{}, f.err
}

func TestWrite_DefaultsSchemaVersionAndMarshalsPayload(t *testing.T) {
	f := &fakeExec{}
	require.NoError(t, Write(context.Background(), f, Message{
		RoutingKey: "payment.succeeded", AggregateID: "11111111-1111-1111-1111-111111111111", Payload: map[string]int{"n": 1},
	}))
	assert.Contains(t, f.sql, "INSERT INTO outbox_events")
	assert.Equal(t, 1, f.args[2], "schemaVersion default 1")
	assert.JSONEq(t, `{"n":1}`, string(f.args[3].([]byte)))
}

func TestWrite_RejectsMissingFieldsAndBadPayload(t *testing.T) {
	f := &fakeExec{}
	assert.Error(t, Write(context.Background(), f, Message{AggregateID: "x"}))
	assert.Error(t, Write(context.Background(), f, Message{RoutingKey: "k"}))
	assert.Error(t, Write(context.Background(), f, Message{RoutingKey: "k", AggregateID: "x", Payload: make(chan int)}))
	assert.Empty(t, f.sql, "tidak ada SQL yang dijalankan untuk input tidak valid")
}

func TestWrite_WrapsExecError(t *testing.T) {
	boom := errors.New("db down")
	err := Write(context.Background(), &fakeExec{err: boom}, Message{RoutingKey: "k", AggregateID: "x"})
	assert.ErrorIs(t, err, boom)
}

func TestBackoff_GrowsAndIsCapped(t *testing.T) {
	for attempts := 0; attempts < 20; attempts++ {
		d := backoff(attempts)
		assert.GreaterOrEqual(t, d, 800*time.Millisecond)
		assert.LessOrEqual(t, d, time.Duration(float64(maxBackoff)*1.2))
	}
	assert.Less(t, backoff(0), 1300*time.Millisecond)
	assert.Greater(t, backoff(8), 200*time.Second)
}
