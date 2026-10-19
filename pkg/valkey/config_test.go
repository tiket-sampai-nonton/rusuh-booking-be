package valkey

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNodesAndAddresses(t *testing.T) {
	c := Config{Server: `[{"host":"valkey","port":6379},{"host":"10.0.0.2","port":6380}]`}
	addrs, err := c.Addresses()
	require.NoError(t, err)
	assert.Equal(t, []string{"valkey:6379", "10.0.0.2:6380"}, addrs)
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want string
	}{
		{"valid", Config{Server: `[{"host":"localhost","port":6379}]`}, ""},
		{"not json", Config{Server: "localhost:6379"}, "VALKEY_SERVER"},
		{"empty array", Config{Server: `[]`}, "kosong"},
		{"bad port", Config{Server: `[{"host":"x","port":0}]`}, "VALKEY_SERVER[0]"},
		{"bad db", Config{Server: `[{"host":"x","port":1}]`, DB: 99}, "VALKEY_DB"},
		{"bad prefix", Config{Server: `[{"host":"x","port":1}]`, PrefixEnv: "a:b"}, "VALKEY_PREFIX_ENV"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if tt.want == "" {
				assert.NoError(t, err)
				return
			}
			assert.ErrorContains(t, err, tt.want)
		})
	}
}

func TestKey(t *testing.T) {
	assert.Equal(t, "q:{e:1}:seq", Config{}.Key("q:{e:1}:seq"))
	assert.Equal(t, "preprod:q:{e:1}:seq", Config{PrefixEnv: "preprod"}.Key("q:{e:1}:seq"))
}
