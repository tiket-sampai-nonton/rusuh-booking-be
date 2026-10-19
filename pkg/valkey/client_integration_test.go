//go:build integration

package valkey

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClient_Ping(t *testing.T) {
	server := os.Getenv("TEST_VALKEY_SERVER")
	if server == "" {
		t.Skip("TEST_VALKEY_SERVER tidak diset")
	}
	ctx := context.Background()
	c, err := New(ctx, Config{Server: server, PrefixEnv: "it"})
	require.NoError(t, err)
	defer c.Close()

	assert.NoError(t, c.Ping(ctx))
	assert.Equal(t, "valkey", c.Name())

	key := c.Key("rbt:it:k")
	require.NoError(t, c.VK().Do(ctx, c.VK().B().Set().Key(key).Value("v").ExSeconds(10).Build()).Error())
	got, err := c.VK().Do(ctx, c.VK().B().Get().Key(key).Build()).ToString()
	require.NoError(t, err)
	assert.Equal(t, "v", got)
}
