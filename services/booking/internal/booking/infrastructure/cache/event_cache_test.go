package cache

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

// fakeSource counts the calls that reach the source repository. Only the methods the cache reads are implemented;
// the embedded nil interface panics if the cache forwards anything else.
type fakeSource struct {
	outbound.IEventRepo
	detailCalls atomic.Int32
	listCalls   atomic.Int32
	gate        chan struct{} // when set, GetDetail blocks until it is closed (keeps the calls in flight together)
}

func (f *fakeSource) GetDetail(_ context.Context, id string) (entity.EventDetail, error) {
	f.detailCalls.Add(1)
	if f.gate != nil {
		<-f.gate
	}
	return entity.EventDetail{Event: entity.Event{ID: id}}, nil
}

func (f *fakeSource) ListEvents(_ context.Context, _ dto.EventListQuery) ([]entity.Event, error) {
	f.listCalls.Add(1)
	return []entity.Event{{ID: "a"}}, nil
}

// fakeClock is a settable clock shared by the cache under test.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func TestEventReadCache_SingleflightAndTTL(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)}
	src := &fakeSource{gate: make(chan struct{})}
	c := NewEventReadCache(src, 2*time.Second, clk.now)
	ctx := context.Background()

	// 50 concurrent reads of one id while the first load is still in flight: one source call, all callers served.
	const callers = 50
	var wg sync.WaitGroup
	errs := make(chan error, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d, err := c.GetDetail(ctx, "event-1")
			if err == nil && d.Event.ID != "event-1" {
				err = assert.AnError
			}
			errs <- err
		}()
	}
	// Give every goroutine time to join the in-flight load before releasing it.
	time.Sleep(100 * time.Millisecond)
	close(src.gate)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	assert.Equal(t, int32(1), src.detailCalls.Load(), "50 concurrent reads must share one source call")

	// Inside the TTL the value is served from memory.
	clk.advance(time.Second)
	_, err := c.GetDetail(ctx, "event-1")
	require.NoError(t, err)
	assert.Equal(t, int32(1), src.detailCalls.Load(), "a read inside the 2 s TTL must not reach the source")

	// After the TTL the source is asked again.
	clk.advance(1100 * time.Millisecond) // total 2.1 s since the load
	_, err = c.GetDetail(ctx, "event-1")
	require.NoError(t, err)
	assert.Equal(t, int32(2), src.detailCalls.Load(), "after 2.1 s the entry is expired and reloaded")

	// Different ids are cached separately.
	_, err = c.GetDetail(ctx, "event-2")
	require.NoError(t, err)
	assert.Equal(t, int32(3), src.detailCalls.Load(), "another id is a separate key")
}

func TestEventReadCache_ListKeyIsTheQuery(t *testing.T) {
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 11, 30, 0, 0, 0, 0, time.UTC)
	base := dto.EventListQuery{Status: "AVAILABLE", City: "Jakarta", Timeframe: "THIS_MONTH", Sort: "DATE_ASC",
		Genres: []string{"POP", "ROCK"}, StartUTC: &start, EndUTC: &end}

	t.Run("equal queries share a key even with other pointers and genre order", func(t *testing.T) {
		s2, e2 := start, end
		other := base
		other.StartUTC, other.EndUTC = &s2, &e2
		other.Genres = []string{"ROCK", "POP"}
		assert.Equal(t, listKey(base), listKey(other))
	})

	t.Run("the caller's genre slice is not reordered", func(t *testing.T) {
		q := base
		q.Genres = []string{"ROCK", "POP"}
		_ = listKey(q)
		assert.Equal(t, []string{"ROCK", "POP"}, q.Genres)
	})

	t.Run("every filter field changes the key", func(t *testing.T) {
		later := end.AddDate(0, 0, 1)
		cases := map[string]dto.EventListQuery{
			"status":    {Status: "SOLD_OUT", City: base.City, Timeframe: base.Timeframe, Sort: base.Sort, Genres: base.Genres, StartUTC: &start, EndUTC: &end},
			"city":      {Status: base.Status, City: "Bandung", Timeframe: base.Timeframe, Sort: base.Sort, Genres: base.Genres, StartUTC: &start, EndUTC: &end},
			"timeframe": {Status: base.Status, City: base.City, Timeframe: "UPCOMING", Sort: base.Sort, Genres: base.Genres, StartUTC: &start, EndUTC: &end},
			"sort":      {Status: base.Status, City: base.City, Timeframe: base.Timeframe, Sort: "PRICE_ASC", Genres: base.Genres, StartUTC: &start, EndUTC: &end},
			"genres":    {Status: base.Status, City: base.City, Timeframe: base.Timeframe, Sort: base.Sort, Genres: []string{"POP"}, StartUTC: &start, EndUTC: &end},
			"start":     {Status: base.Status, City: base.City, Timeframe: base.Timeframe, Sort: base.Sort, Genres: base.Genres, StartUTC: nil, EndUTC: &end},
			"end":       {Status: base.Status, City: base.City, Timeframe: base.Timeframe, Sort: base.Sort, Genres: base.Genres, StartUTC: &start, EndUTC: &later},
		}
		want := listKey(base)
		for name, q := range cases {
			assert.NotEqual(t, want, listKey(q), "field %s must be part of the key", name)
		}
	})

	t.Run("a city that looks like another field cannot collide", func(t *testing.T) {
		a := dto.EventListQuery{City: "A|B"}
		b := dto.EventListQuery{City: "A", Timeframe: "B"}
		assert.NotEqual(t, listKey(a), listKey(b))
	})

	t.Run("ListEvents serves a repeated query from memory", func(t *testing.T) {
		clk := &fakeClock{t: time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)}
		src := &fakeSource{}
		c := NewEventReadCache(src, 2*time.Second, clk.now)
		ctx := context.Background()
		_, err := c.ListEvents(ctx, base)
		require.NoError(t, err)
		_, err = c.ListEvents(ctx, base)
		require.NoError(t, err)
		assert.Equal(t, int32(1), src.listCalls.Load())
	})
}
