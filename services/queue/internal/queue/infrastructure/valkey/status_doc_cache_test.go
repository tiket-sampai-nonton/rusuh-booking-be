package valkey

import (
	"context"
	"errors"
	"fmt"
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
	store.EXPECT().GetStatusDoc(gomock.Any(), "ev1").Return(raw, true, nil).Times(2)

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
	store.EXPECT().GetStatusDoc(gomock.Any(), "ev9").Return(nil, false, nil).Times(2)

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
	store.EXPECT().GetStatusDoc(gomock.Any(), "idle").Return(raw, true, nil).Times(2)
	store.EXPECT().GetStatusDoc(gomock.Any(), "busy").Return(raw, true, nil).Times(1)

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

// slotCount reports how many event slots the cache holds.
func slotCount(c *StatusDocCache) int {
	n := 0
	c.slots.Range(func(_, _ any) bool { n++; return true })
	return n
}

// An unauthenticated caller can name any event id, so reads of ids with no document (or whose read fails) must not leave
// a slot behind: 10,000 distinct absent ids keep the slot count at zero.
func TestStatusDocCache_AbsentIDsLeaveNoSlots(t *testing.T) {
	ctrl := gomock.NewController(t)
	store := mock_outbound.NewMockIQueueStore(ctrl)
	ctx := context.Background()
	store.EXPECT().GetStatusDoc(gomock.Any(), gomock.Any()).Return(nil, false, nil).Times(10_000)
	store.EXPECT().GetStatusDoc(gomock.Any(), gomock.Any()).Return(nil, false, errors.New("valkey down")).Times(1_000)

	c := NewStatusDocCache(store, StatusDocRefresh, (&stepClock{t: time.Unix(1_700_000_000, 0)}).now)
	for i := 0; i < 10_000; i++ {
		_, _, ok, err := c.Get(ctx, fmt.Sprintf("absent-%d", i))
		require.NoError(t, err)
		require.False(t, ok)
	}
	assert.Equal(t, 0, slotCount(c), "absent ids must not leave slots")

	for i := 0; i < 1_000; i++ {
		_, _, _, err := c.Get(ctx, fmt.Sprintf("failing-%d", i))
		require.Error(t, err)
	}
	assert.Equal(t, 0, slotCount(c), "failed reads must not leave slots")
}

// The shared Valkey read runs detached from the leader's context: a leader cancelled while the read is in flight does not
// fail the follower that joined it, which gets the document.
func TestStatusDocCache_LeaderCancelDoesNotFailFollower(t *testing.T) {
	ctrl := gomock.NewController(t)
	store := mock_outbound.NewMockIQueueStore(ctrl)
	raw := []byte(`{"eventId":"ev2","saleState":"OPEN"}`)
	started := make(chan struct{})
	release := make(chan struct{})
	store.EXPECT().GetStatusDoc(gomock.Any(), "ev2").DoAndReturn(func(ctx context.Context, _ string) ([]byte, bool, error) {
		close(started)
		<-release // the read is still in flight when the leader is cancelled
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		return raw, true, nil
	}).Times(1)

	c := NewStatusDocCache(store, StatusDocRefresh, (&stepClock{t: time.Unix(1_700_000_000, 0)}).now)
	leaderCtx, cancel := context.WithCancel(context.Background())
	type result struct {
		raw []byte
		ok  bool
		err error
	}
	leaderDone := make(chan result, 1)
	followerDone := make(chan result, 1)
	go func() {
		got, _, ok, err := c.Get(leaderCtx, "ev2")
		leaderDone <- result{got, ok, err}
	}()
	<-started
	go func() {
		got, _, ok, err := c.Get(context.Background(), "ev2")
		followerDone <- result{got, ok, err}
	}()
	time.Sleep(50 * time.Millisecond) // give the follower time to join the in-flight read
	cancel()
	close(release)

	for name, ch := range map[string]chan result{"leader": leaderDone, "follower": followerDone} {
		r := <-ch
		require.NoError(t, r.err, name)
		require.True(t, r.ok, name)
		assert.Equal(t, raw, r.raw, name)
	}
}
