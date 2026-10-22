package httpx

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/logger"
)

func accessChain(t *testing.T, mux *http.ServeMux) (http.Handler, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	l, err := logger.New(&buf, logger.Config{Level: "debug", Format: "json", Env: "test"}, "svc", "api")
	require.NoError(t, err)
	return Chain(mux, RequestID(), AccessLog(l), Recover(l)), &buf
}

func lastRecord(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	var rec map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[len(lines)-1]), &rec))
	return rec
}

func TestAccessLog_FieldsAndRouteTemplate(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /items/{id}", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("hello")) })
	h, buf := accessChain(t, mux)

	req := httptest.NewRequest(http.MethodGet, "/items/42?token=rahasia", nil)
	req.Header.Set(HeaderRequestID, "req-1")
	req.Header.Set("X-Amzn-Trace-Id", amznHeader)
	h.ServeHTTP(httptest.NewRecorder(), req)

	rec := lastRecord(t, buf)
	assert.Equal(t, "INFO", rec["level"])
	assert.Equal(t, "GET", rec["method"])
	assert.Equal(t, "/items/{id}", rec["route"], "template, bukan path asli")
	assert.EqualValues(t, 200, rec["status"])
	assert.EqualValues(t, 5, rec["bytes"])
	assert.Contains(t, rec, "duration_ms")
	assert.Equal(t, "req-1", rec["requestId"])
	assert.Equal(t, amznRoot, rec["trace_id"])
	assert.Equal(t, "test", rec["env"])
	assert.NotContains(t, buf.String(), "rahasia", "query string tidak dilog")
	assert.NotContains(t, buf.String(), "/items/42")
}

func TestAccessLog_LevelByStatus(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /bad", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadRequest) })
	mux.HandleFunc("GET /boom", func(http.ResponseWriter, *http.Request) { panic("boom") })
	mux.HandleFunc("GET /empty", func(http.ResponseWriter, *http.Request) {})
	h, buf := accessChain(t, mux)

	for path, want := range map[string]struct {
		level  string
		status float64
	}{"/bad": {"WARN", 400}, "/boom": {"ERROR", 500}, "/empty": {"INFO", 200}, "/tidak-ada": {"WARN", 404}} {
		buf.Reset()
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
		lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
		var rec map[string]any
		require.NoError(t, json.Unmarshal([]byte(lines[len(lines)-1]), &rec), path)
		assert.Equal(t, "request", rec["msg"], path)
		assert.Equal(t, want.level, rec["level"], path)
		assert.Equal(t, want.status, rec["status"], path)
	}
}

func TestAccessLog_UnmatchedRoute(t *testing.T) {
	h, buf := accessChain(t, http.NewServeMux())
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/acak/123456", nil))

	assert.Equal(t, "unmatched", lastRecord(t, buf)["route"])
	assert.NotContains(t, buf.String(), "123456")
}

func TestAccessLog_SkipsHealthAndMetrics(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(http.ResponseWriter, *http.Request) {})
	mux.HandleFunc("GET /metrics", func(http.ResponseWriter, *http.Request) {})
	h, buf := accessChain(t, mux)

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/health", nil))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/metrics", nil))

	assert.Empty(t, buf.String())
}

// Handler SSE memakai w.(http.Flusher); wrapper harus meneruskannya.
func TestStatusWriter_ForwardsFlush(t *testing.T) {
	var isFlusher bool
	mux := http.NewServeMux()
	mux.HandleFunc("GET /stream", func(w http.ResponseWriter, _ *http.Request) {
		f, ok := w.(http.Flusher)
		isFlusher = ok
		if ok {
			f.Flush()
		}
	})
	h, _ := accessChain(t, mux)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/stream", nil))

	assert.True(t, isFlusher)
	assert.True(t, rec.Flushed)
}

func TestStatusWriter_RecordsFirstStatusOnly(t *testing.T) {
	sw := NewStatusWriter(httptest.NewRecorder())
	assert.Equal(t, 200, sw.Status, "default bila handler tidak menulis")
	sw.WriteHeader(http.StatusCreated)
	sw.WriteHeader(http.StatusTeapot)
	_, _ = sw.Write([]byte("abc"))
	assert.Equal(t, http.StatusCreated, sw.Status)
	assert.EqualValues(t, 3, sw.Bytes)
	assert.Same(t, sw, WrapStatus(sw), "tidak dibungkus dua kali")
}
