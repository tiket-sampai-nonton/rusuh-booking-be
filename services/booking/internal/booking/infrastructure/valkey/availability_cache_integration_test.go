//go:build integration

package valkey

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pkgvalkey "github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/valkey"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

func TestAvailabilityCache_ReadsSnapshotAndCountsConnections(t *testing.T) {
	server := os.Getenv("TEST_VALKEY_SERVER")
	if server == "" {
		t.Skip("TEST_VALKEY_SERVER tidak diset")
	}
	ctx := context.Background()
	c, err := pkgvalkey.New(ctx, pkgvalkey.Config{Server: server, PrefixEnv: "itb" + fmt.Sprint(time.Now().UnixNano()%1e6)})
	require.NoError(t, err)
	t.Cleanup(c.Close)
	cache := NewAvailabilityCache(c.VK())
	// The cache uses unprefixed keys: make them unique per run and remove them afterwards.
	run := fmt.Sprint(time.Now().UnixNano())
	ev1, bad, inst := "ev1-"+run, "bad-"+run, "inst-"+run
	t.Cleanup(func() {
		for _, k := range []string{"booking:avail:" + ev1, "booking:avail:" + bad, "booking:sse:conn:" + inst} {
			_ = c.VK().Do(context.Background(), c.VK().B().Del().Key(k).Build()).Error()
		}
	})

	none, err := cache.GetAvailability(ctx, "no-such-event")
	require.NoError(t, err)
	assert.Nil(t, none, "tanpa snapshot: nil, bukan error")

	raw, _ := json.Marshal([]outbound.CategoryAvailabilitySnap{{CategoryID: "c1", Available: 7}})
	require.NoError(t, c.VK().Do(ctx, c.VK().B().Set().Key("booking:avail:"+ev1).Value(string(raw)).Build()).Error())
	got, err := cache.GetAvailability(ctx, ev1)
	require.NoError(t, err)
	assert.Equal(t, []outbound.CategoryAvailabilitySnap{{CategoryID: "c1", Available: 7}}, got)

	require.NoError(t, c.VK().Do(ctx, c.VK().B().Set().Key("booking:avail:"+bad).Value("{bukan json").Build()).Error())
	_, err = cache.GetAvailability(ctx, bad)
	assert.Error(t, err, "snapshot rusak dilaporkan")

	require.NoError(t, cache.SetAvailability(ctx, ev1, []outbound.CategoryAvailabilitySnap{{CategoryID: "c9", Available: 2}}, time.Minute))
	got, err = cache.GetAvailability(ctx, ev1)
	require.NoError(t, err)
	assert.Equal(t, []outbound.CategoryAvailabilitySnap{{CategoryID: "c9", Available: 2}}, got, "snapshot ditulis lalu terbaca")

	n, err := cache.IncrConn(ctx, inst)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	n, err = cache.IncrConn(ctx, inst)
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	require.NoError(t, cache.DecrConn(ctx, inst))
	n, err = cache.IncrConn(ctx, inst)
	require.NoError(t, err)
	assert.Equal(t, 2, n)
}
