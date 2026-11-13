package valkey

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	mock_outbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/_mock/queue/port/outbound"
)

// stepClock is a settable clock for the status document cache tests.
type stepClock struct{ t time.Time }

func (c *stepClock) now() time.Time { return c.t }

// C8.9 (D-C8.9): the status document is reused for 250 ms; a burst of reads inside that window costs one Valkey read.
func TestStatusDocCache_RefreshesEvery250ms(t *testing.T) {
	ctrl := gomock.NewController(t)
	store := mock_outbound.NewMockIQueueStore(ctrl)
	ctx := context.Background()
	raw := []byte(`{"eventId":"ev1","saleState":"OPEN"}`)
	store.EXPECT().GetStatusDoc(ctx, "ev1").Return(raw, true, nil).Times(2)

	clk := &stepClock{t: time.Unix(1_700_000_000, 0)}
	c := NewStatusDocCache(store, StatusDocRefresh, clk.now)
	start := clk.t
	for i := 0; i < 100; i++ {
		clk.t = start.Add(time.Duration(i*2) * time.Millisecond) // 100 reads spread over 198 ms
		got, doc, ok, err := c.Get(ctx, "ev1")
		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, raw, got)
		assert.Equal(t, "OPEN", doc.SaleState)
	}
	clk.t = start.Add(260 * time.Millisecond)
	_, _, ok, err := c.Get(ctx, "ev1")
	require.NoError(t, err)
	assert.True(t, ok)
}

// An event with no document is a cache miss every time: the next read asks Valkey again.
func TestStatusDocCache_AbsentDocIsNotCached(t *testing.T) {
	ctrl := gomock.NewController(t)
	store := mock_outbound.NewMockIQueueStore(ctrl)
	ctx := context.Background()
	store.EXPECT().GetStatusDoc(ctx, "ev9").Return(nil, false, nil).Times(2)

	c := NewStatusDocCache(store, StatusDocRefresh, (&stepClock{t: time.Unix(1_700_000_000, 0)}).now)
	for i := 0; i < 2; i++ {
		_, _, ok, err := c.Get(ctx, "ev9")
		require.NoError(t, err)
		assert.False(t, ok)
	}
}

// Memory stays bounded: a snapshot unread for over a minute is dropped by the next refresh pass, and the next read of
// that event goes back to Valkey.
func TestStatusDocCache_DropsIdleEvents(t *testing.T) {
	ctrl := gomock.NewController(t)
	store := mock_outbound.NewMockIQueueStore(ctrl)
	ctx := context.Background()
	raw := []byte(`{"eventId":"x"}`)
	store.EXPECT().GetStatusDoc(ctx, "idle").Return(raw, true, nil).Times(2)
	store.EXPECT().GetStatusDoc(ctx, "busy").Return(raw, true, nil).Times(1)

	clk := &stepClock{t: time.Unix(1_700_000_000, 0)}
	c := NewStatusDocCache(store, StatusDocRefresh, clk.now)
	_, _, _, err := c.Get(ctx, "idle")
	require.NoError(t, err)
	clk.t = clk.t.Add(2 * time.Minute)
	_, _, _, err = c.Get(ctx, "busy")
	require.NoError(t, err)
	_, _, ok, err := c.Get(ctx, "idle")
	require.NoError(t, err)
	assert.True(t, ok)
}
