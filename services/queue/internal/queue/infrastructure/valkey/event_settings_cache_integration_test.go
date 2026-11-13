//go:build integration

package valkey

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
)

// stubSettingsSource is a minimal IEventSettingsRepo stub for cache tests.
type stubSettingsSource struct {
	calls  int
	result entity.EventSettings
	found  bool
	err    error
}

func (s *stubSettingsSource) Get(_ context.Context, _ string) (entity.EventSettings, bool, error) {
	s.calls++
	return s.result, s.found, s.err
}

func (s *stubSettingsSource) ListEventIDs(_ context.Context) ([]string, error) {
	return []string{"ev-list"}, nil
}

func TestEventSettingsCache(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	newCache := func(src *stubSettingsSource) *EventSettingsCache {
		return NewEventSettingsCache(store.c, store.keyer, src)
	}

	t.Run("cache miss: source dipanggil dan hasil disimpan", func(t *testing.T) {
		src := &stubSettingsSource{
			result: entity.EventSettings{EventID: "ev-c1", ActiveSlotCapacity: 200},
			found:  true,
		}
		c := newCache(src)

		s, found, err := c.Get(ctx, "ev-c1")
		require.NoError(t, err)
		assert.True(t, found)
		assert.Equal(t, 200, s.ActiveSlotCapacity)
		assert.Equal(t, 1, src.calls, "source harus dipanggil sekali pada cache miss")
	})

	t.Run("cache hit: source tidak dipanggil lagi", func(t *testing.T) {
		src := &stubSettingsSource{
			result: entity.EventSettings{EventID: "ev-c2", ActiveSlotCapacity: 300},
			found:  true,
		}
		c := newCache(src)

		_, _, _ = c.Get(ctx, "ev-c2")        // populate cache
		s, found, err := c.Get(ctx, "ev-c2") // should hit cache
		require.NoError(t, err)
		assert.True(t, found)
		assert.Equal(t, 300, s.ActiveSlotCapacity)
		assert.Equal(t, 1, src.calls, "source hanya boleh dipanggil sekali — kunjungan kedua dari cache")
	})

	t.Run("source not-found tidak disimpan ke cache", func(t *testing.T) {
		src := &stubSettingsSource{found: false}
		c := newCache(src)

		_, found, err := c.Get(ctx, "ev-c3-missing")
		require.NoError(t, err)
		assert.False(t, found)

		_, found2, _ := c.Get(ctx, "ev-c3-missing")
		assert.False(t, found2)
		assert.Equal(t, 2, src.calls, "not-found tidak di-cache: source selalu dipanggil")
	})

	t.Run("source error tidak disimpan ke cache", func(t *testing.T) {
		src := &stubSettingsSource{err: errStubFail}
		c := newCache(src)

		_, _, err := c.Get(ctx, "ev-c4-err")
		assert.ErrorIs(t, err, errStubFail)
		assert.Equal(t, 1, src.calls)
	})

	t.Run("ListEventIDs didelegasikan ke source", func(t *testing.T) {
		src := &stubSettingsSource{}
		c := newCache(src)

		ids, err := c.ListEventIDs(ctx)
		require.NoError(t, err)
		assert.Equal(t, []string{"ev-list"}, ids)
	})
}

// errStubFail is a sentinel error used by cache tests.
var errStubFail = stubError("stub postgres failure")

type stubError string

func (e stubError) Error() string { return string(e) }

// TestEventSettingsCache_InvalidateDropsKey proves that Invalidate removes the cached row, so the next Get goes back
// to Postgres and sees a change committed by a PUT (F-qcache).
func TestEventSettingsCache_InvalidateDropsKey(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	src := &stubSettingsSource{
		result: entity.EventSettings{EventID: "ev-inv", ActiveSlotCapacity: 200},
		found:  true,
	}
	c := NewEventSettingsCache(store.c, store.keyer, src)

	_, _, err := c.Get(ctx, "ev-inv") // populate cache
	require.NoError(t, err)
	require.Equal(t, 1, src.calls)

	require.NoError(t, c.Invalidate(ctx, "ev-inv"))

	exists, err := store.c.Do(ctx, store.c.B().Exists().Key(c.cacheKey("ev-inv")).Build()).AsInt64()
	require.NoError(t, err)
	assert.Equal(t, int64(0), exists, "key cache harus terhapus setelah Invalidate")

	src.result.ActiveSlotCapacity = 350 // the PUT committed a new value
	s, found, err := c.Get(ctx, "ev-inv")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, 2, src.calls, "pembacaan berikutnya harus ke source")
	assert.Equal(t, 350, s.ActiveSlotCapacity)
}
