package logger

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew_EnvField(t *testing.T) {
	buf := newBufWithEnv(t, "staging")
	buf.l.Info("x")

	var rec map[string]any
	require.NoError(t, json.Unmarshal(buf.b.Bytes(), &rec))
	assert.Equal(t, "staging", rec["env"])
	assert.Equal(t, "svc", rec["service"])
	assert.Equal(t, "api", rec["verb"])
}

func TestNew_NoEnvWhenEmpty(t *testing.T) {
	b := newBufWithEnv(t, "")
	b.l.Info("x")
	assert.NotContains(t, b.b.String(), `"env"`)
}

func TestContextWith_AddsAttrsOnContextCalls(t *testing.T) {
	b := newBufWithEnv(t, "local")
	ctx := ContextWith(context.Background(), "requestId", "r-1", "trace_id", "t-1")

	b.l.InfoContext(ctx, "dengan ctx")
	var rec map[string]any
	require.NoError(t, json.Unmarshal(b.b.Bytes(), &rec))
	assert.Equal(t, "r-1", rec["requestId"])
	assert.Equal(t, "t-1", rec["trace_id"])

	b.b.Reset()
	b.l.Info("tanpa ctx")
	assert.NotContains(t, b.b.String(), "requestId")
}

func TestContextWith_Accumulates(t *testing.T) {
	b := newBufWithEnv(t, "")
	ctx := ContextWith(ContextWith(context.Background(), "a", "1"), "b", "2")

	b.l.With("w", "3").WithGroup("g").InfoContext(ctx, "x")

	var rec map[string]any
	require.NoError(t, json.Unmarshal(b.b.Bytes(), &rec))
	assert.Equal(t, "3", rec["w"])
	assert.Equal(t, map[string]any{"a": "1", "b": "2"}, rec["g"], "atribut ctx ikut group aktif")
}

// Atribut dari ctx tetap melewati redaksi 4.4.
func TestContextWith_StillRedacted(t *testing.T) {
	b := newBufWithEnv(t, "")
	ctx := ContextWith(context.Background(), "email", secretValue)
	b.l.InfoContext(ctx, "x")
	assert.NotContains(t, b.b.String(), secretValue)
}

type testLogger struct {
	l *slog.Logger
	b *bytes.Buffer
}

func newBufWithEnv(t *testing.T, env string) testLogger {
	t.Helper()
	var buf bytes.Buffer
	l, err := New(&buf, Config{Level: "debug", Format: "json", Env: env}, "svc", "api")
	require.NoError(t, err)
	return testLogger{l: l, b: &buf}
}
