package http_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

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
	req := httptest.NewRequest(http.MethodGet, "/api/v1/booking-sessions/s-1/stream", nil)
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.code)
	assert.False(t, stream.called, "usecase must not run when the writer cannot flush")
}

func TestSSEHandler_Flusher_SetsSSEHeaders(t *testing.T) {
	stream := &fakeStream{}
	mux := newSSEServer(stream)

	// httptest.ResponseRecorder implements http.Flusher and records Flushed.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/booking-sessions/s-1/stream", nil)
	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "text/event-stream", rec.Header().Get("Content-Type"))
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	assert.Equal(t, "no", rec.Header().Get("X-Accel-Buffering"))
	assert.True(t, stream.called)
	assert.True(t, rec.Flushed, "handler must flush through the ResponseWriter")
	assert.Contains(t, rec.Body.String(), "event: timer")
}
