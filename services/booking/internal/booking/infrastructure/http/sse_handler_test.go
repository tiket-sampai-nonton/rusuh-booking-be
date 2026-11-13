package http_test

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	bookinghttp "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/infrastructure/http"
)

// fakeStream is a controllable IStreamSessionUsecase.
type fakeStream struct {
	err     error
	called  bool
	wrote   string
	flushed bool
}

func (f *fakeStream) Stream(_ context.Context, _, _ string, w io.Writer, flush func()) error {
	f.called = true
	if f.err != nil {
		return f.err
	}
	_, _ = io.WriteString(w, "event: timer\ndata: {}\n\n")
	f.wrote = "event: timer\ndata: {}\n\n"
	flush()
	f.flushed = true
	return nil
}

// nonFlushWriter is an http.ResponseWriter that deliberately does NOT implement
// http.Flusher (unlike httptest.ResponseRecorder, which does).
type nonFlushWriter struct {
	header http.Header
	code   int
	body   []byte
}

func (w *nonFlushWriter) Header() http.Header {
	if w.header == nil {
		w.header = http.Header{}
	}
	return w.header
}
func (w *nonFlushWriter) WriteHeader(code int) { w.code = code }
func (w *nonFlushWriter) Write(b []byte) (int, error) {
	if w.code == 0 {
		w.code = http.StatusOK
	}
	w.body = append(w.body, b...)
	return len(b), nil
}

func newSSEServer(stream *fakeStream) *http.ServeMux {
	mux := http.NewServeMux()
	bookinghttp.NewSSEHandler(stream).Register(mux)
	return mux
}

func TestSSEHandler_NoFlusher_Returns503(t *testing.T) {
	stream := &fakeStream{}
	mux := newSSEServer(stream)

	w := &nonFlushWriter{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/booking-sessions/11111111-1111-1111-1111-111111111111/stream", nil)
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.code)
	assert.False(t, stream.called, "usecase must not run when the writer cannot flush")
}

func TestSSEHandler_Flusher_SetsSSEHeaders(t *testing.T) {
	stream := &fakeStream{}
	mux := newSSEServer(stream)

	// httptest.ResponseRecorder implements http.Flusher and records Flushed.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/booking-sessions/11111111-1111-1111-1111-111111111111/stream", nil)
	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "text/event-stream", rec.Header().Get("Content-Type"))
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	assert.Equal(t, "no", rec.Header().Get("X-Accel-Buffering"))
	assert.True(t, stream.called)
	assert.True(t, rec.Flushed, "handler must flush through the ResponseWriter")
	assert.Contains(t, rec.Body.String(), "event: timer")
}

// REQ-4: a malformed sessionId must be rejected with 400 VALIDATION_ERROR BEFORE the SSE 200 headers are
// committed. Because SSE flushes the 200 status up front, the format check has to run in the handler (the
// usecase guard alone can never change a status that is already on the wire).
func TestSSEHandler_MalformedSessionID_Returns400(t *testing.T) {
	stream := &fakeStream{}
	mux := newSSEServer(stream)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/booking-sessions/not-a-uuid/stream", nil)
	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "VALIDATION_ERROR")
	assert.False(t, stream.called, "usecase must not run for a malformed sessionId")
}

// tickingStream writes one timer frame per interval until the request context ends, like a live session.
type tickingStream struct{ every time.Duration }

func (s *tickingStream) Stream(ctx context.Context, _, _ string, w io.Writer, flush func()) error {
	ticker := time.NewTicker(s.every)
	defer ticker.Stop()
	for {
		if _, err := io.WriteString(w, "event: timer\ndata: {}\n\n"); err != nil {
			return err
		}
		flush()
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// C7.5: the server's WriteTimeout must not cut a live stream, so the handler clears the write deadline before it streams.
func TestSSEHandler_ClearsWriteDeadline(t *testing.T) {
	mux := http.NewServeMux()
	bookinghttp.NewSSEHandler(&tickingStream{every: 200 * time.Millisecond}).Register(mux)
	srv := httptest.NewUnstartedServer(mux)
	srv.Config.WriteTimeout = time.Second
	srv.Start()
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/v1/booking-sessions/11111111-1111-1111-1111-111111111111/stream", nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() {
		_ = resp.Body.Close()
	}()

	start := time.Now()
	late := 0
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		if scanner.Text() == "event: timer" && time.Since(start) > 1500*time.Millisecond {
			late++
		}
	}
	assert.Positive(t, late, "timer frames must still arrive after the 1 s WriteTimeout")
}
