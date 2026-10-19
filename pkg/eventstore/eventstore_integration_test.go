//go:build integration

package eventstore

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAppend_ConcurrentWritersOnlyOneWins: 20 penulis paralel dengan expectedVersion sama;
// tepat satu berhasil, sisanya ErrConcurrency, dan log tetap rapat tanpa celah.
func TestAppend_ConcurrentWritersOnlyOneWins(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN tidak diset")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	_, err = pool.Exec(ctx, `DROP TABLE IF EXISTS it_events;
		CREATE TABLE it_events (stream_id uuid NOT NULL, version int NOT NULL, event_type text NOT NULL,
		  schema_version smallint NOT NULL DEFAULT 1, payload jsonb NOT NULL, metadata jsonb NOT NULL DEFAULT '{}',
		  occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(), global_seq bigint GENERATED ALWAYS AS IDENTITY,
		  PRIMARY KEY (stream_id, version))`)
	require.NoError(t, err)
	defer func() { _, _ = pool.Exec(ctx, `DROP TABLE IF EXISTS it_events`) }()

	s, err := New("it_events")
	require.NoError(t, err)
	stream := "6f1c0e7a-0000-4000-8000-000000000001"
	_, err = s.Append(ctx, pool, stream, 0, NewEvent{Type: "BookingHeld", Payload: map[string]int{"units": 2}})
	require.NoError(t, err)

	var ok, conflict atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
				_, err := s.Append(ctx, tx, stream, 1, NewEvent{Type: "BookingLocked", Payload: map[string]string{"by": fmt.Sprint(i)}})
				return err
			})
			switch {
			case err == nil:
				ok.Add(1)
			default:
				assert.ErrorIs(t, err, ErrConcurrency)
				conflict.Add(1)
			}
		}(i)
	}
	wg.Wait()
	assert.Equal(t, int32(1), ok.Load())
	assert.Equal(t, int32(19), conflict.Load())

	events, err := s.Load(ctx, pool, stream)
	require.NoError(t, err)
	require.Len(t, events, 2)
	v, err := Replay(events, applierFunc(func(Event) error { return nil }))
	require.NoError(t, err)
	assert.Equal(t, 2, v)

	after, err := s.LoadAfter(ctx, pool, stream, 1)
	require.NoError(t, err)
	assert.Len(t, after, 1)
}

type applierFunc func(Event) error

func (f applierFunc) Apply(e Event) error { return f(e) }
