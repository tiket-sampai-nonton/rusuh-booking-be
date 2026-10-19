package logger

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew_JSONWithStaticAttributes(t *testing.T) {
	var buf bytes.Buffer
	l, err := New(&buf, Config{Level: "debug", Format: "json"}, "booking", "api")
	require.NoError(t, err)

	l.Debug("hello", "k", "v")

	var rec map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &rec))
	assert.Equal(t, "booking", rec["service"])
	assert.Equal(t, "api", rec["verb"])
	assert.Equal(t, "hello", rec["msg"])
}

func TestNew_TextFormatAndLevelFilter(t *testing.T) {
	var buf bytes.Buffer
	l, err := New(&buf, Config{Level: "warn", Format: "text"}, "queue", "worker")
	require.NoError(t, err)

	l.Info("dropped")
	assert.Empty(t, buf.String())
	l.Warn("kept")
	assert.Contains(t, buf.String(), "msg=kept")
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{"defaults", Config{}, false},
		{"lowercase level", Config{Level: "info", Format: "json"}, false},
		{"bad level", Config{Level: "verbose"}, true},
		{"bad format", Config{Level: "info", Format: "xml"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestNew_RejectsBadLevel(t *testing.T) {
	_, err := New(&bytes.Buffer{}, Config{Level: "nope"}, "s", "v")
	assert.Error(t, err)
}
