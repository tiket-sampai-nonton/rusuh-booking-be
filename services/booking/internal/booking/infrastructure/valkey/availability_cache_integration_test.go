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

	none, err := cache.GetAvailability(ctx, "no-such-event")
	require.NoError(t, err)
	assert.Nil(t, none, "tanpa snapshot: nil, bukan error")

	raw, _ := json.Marshal([]outbound.CategoryAvailabilitySnap{{CategoryID: "c1", Available: 7}})
	require.NoError(t, c.VK().Do(ctx, c.VK().B().Set().Key("booking:avail:ev1").Value(string(raw)).Build()).Error())
	got, err := cache.GetAvailability(ctx, "ev1")
	require.NoError(t, err)
	assert.Equal(t, []outbound.CategoryAvailabilitySnap{{CategoryID: "c1", Available: 7}}, got)

	require.NoError(t, c.VK().Do(ctx, c.VK().B().Set().Key("booking:avail:bad").Value("{bukan json").Build()).Error())
	_, err = cache.GetAvailability(ctx, "bad")
	assert.Error(t, err, "snapshot rusak dilaporkan")

	n, err := cache.IncrConn(ctx, "inst-1")
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	n, err = cache.IncrConn(ctx, "inst-1")
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	require.NoError(t, cache.DecrConn(ctx, "inst-1"))
	n, err = cache.IncrConn(ctx, "inst-1")
	require.NoError(t, err)
	assert.Equal(t, 2, n)
}
