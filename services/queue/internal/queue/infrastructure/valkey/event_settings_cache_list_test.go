package valkey

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// countingActiveSource counts ListActiveEventIDs calls on the source; the rest of the interface is never used here.
type countingActiveSource struct {
	outbound.IEventSettingsRepo
	calls int
	ids   []string
}

func (s *countingActiveSource) ListActiveEventIDs(context.Context, time.Time) ([]string, error) {
	s.calls++
	return s.ids, nil
}

// TC-C8.10: the active-event list is cached in process for 5 s, so the four worker jobs share one read per window.
func TestEventSettingsCache_ListActiveEventIDsCachedFiveSeconds(t *testing.T) {
	src := &countingActiveSource{ids: []string{"ev1", "ev2"}}
	c := NewEventSettingsCache(nil, nil, src)
	ctx := context.Background()
	t0 := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)

	ids, err := c.ListActiveEventIDs(ctx, t0)
	require.NoError(t, err)
	assert.Equal(t, []string{"ev1", "ev2"}, ids)
	_, err = c.ListActiveEventIDs(ctx, t0.Add(time.Second))
	require.NoError(t, err)
	_, err = c.ListActiveEventIDs(ctx, t0.Add(4900*time.Millisecond))
	require.NoError(t, err)
	assert.Equal(t, 1, src.calls, "reads inside the 5 s window are served from the cache")

	ids, err = c.ListActiveEventIDs(ctx, t0.Add(5*time.Second))
	require.NoError(t, err)
	assert.Equal(t, []string{"ev1", "ev2"}, ids)
	assert.Equal(t, 2, src.calls, "a read 5 s later goes to Postgres again")

	// A caller cannot change what the cache holds.
	ids[0] = "mutated"
	again, err := c.ListActiveEventIDs(ctx, t0.Add(5*time.Second))
	require.NoError(t, err)
	assert.Equal(t, []string{"ev1", "ev2"}, again)
}
