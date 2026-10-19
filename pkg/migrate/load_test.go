package migrate

import (
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad_SortsAndPairs(t *testing.T) {
	fsys := fstest.MapFS{
		"m/0002_event_store.up.sql":   {Data: []byte("CREATE TABLE b (id int);")},
		"m/0002_event_store.down.sql": {Data: []byte("DROP TABLE b;")},
		"m/0001_init.up.sql":          {Data: []byte("CREATE TABLE a (id int);")},
		"m/0001_init.down.sql":        {Data: []byte("DROP TABLE a;")},
		"m/embed.go":                  {Data: []byte("package m")},
	}
	got, err := Load(fsys, "m")
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, 1, got[0].Version)
	assert.Equal(t, "init", got[0].Name)
	assert.Equal(t, "event_store", got[1].Name)
	assert.Len(t, got[0].Checksum, 64)
	assert.NotEqual(t, got[0].Checksum, got[1].Checksum)
}

func TestLoad_Errors(t *testing.T) {
	tests := []struct {
		name string
		fs   fstest.MapFS
		want string
	}{
		{"bad name", fstest.MapFS{"m/1_init.up.sql": {Data: []byte("x")}}, "NNNN_nama"},
		{"missing down", fstest.MapFS{"m/0001_init.up.sql": {Data: []byte("x")}}, "wajib punya"},
		{"gap", fstest.MapFS{
			"m/0001_a.up.sql": {Data: []byte("x")}, "m/0001_a.down.sql": {Data: []byte("x")},
			"m/0003_c.up.sql": {Data: []byte("x")}, "m/0003_c.down.sql": {Data: []byte("x")},
		}, "berurutan"},
		{"two names", fstest.MapFS{"m/0001_a.up.sql": {Data: []byte("x")}, "m/0001_b.down.sql": {Data: []byte("x")}}, "dua nama"},
		{"missing dir", fstest.MapFS{}, "read"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(tt.fs, "m")
			assert.ErrorContains(t, err, tt.want)
		})
	}
}

func TestNewRunner_RejectsBadSchema(t *testing.T) {
	_, err := NewRunner(nil, "booking; drop", nil, nil)
	assert.Error(t, err)
	assert.Equal(t, "rbt.migrate.booking", LockKey("booking"))
}
