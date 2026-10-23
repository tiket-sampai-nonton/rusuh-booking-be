package httpx

import (
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// StatusWriter mencatat status dan jumlah byte respons. Mengimplementasikan Flush karena handler SSE
// memakai w.(http.Flusher); Unwrap untuk http.ResponseController.
type StatusWriter struct {
	http.ResponseWriter
	Status int
	Bytes  int64
	wrote  bool
}

// NewStatusWriter wraps w; Status bernilai 200 bila handler tidak menulis apa pun.
func NewStatusWriter(w http.ResponseWriter) *StatusWriter {
	return &StatusWriter{ResponseWriter: w, Status: http.StatusOK}
}

// WrapStatus returns w itself when it is already a StatusWriter, so stacked middleware share one recorder.
func WrapStatus(w http.ResponseWriter) *StatusWriter {
	if sw, ok := w.(*StatusWriter); ok {
		return sw
	}
	return NewStatusWriter(w)
}

// WriteHeader records the first final status (>= 200).
func (w *StatusWriter) WriteHeader(code int) {
	if !w.wrote && code >= http.StatusOK {
		w.Status, w.wrote = code, true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *StatusWriter) Write(b []byte) (int, error) {
	w.wrote = true
	n, err := w.ResponseWriter.Write(b)
	w.Bytes += int64(n)
	return n, err
}

// Flush forwards to the underlying writer when it supports flushing.
func (w *StatusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap returns the underlying writer (http.ResponseController).
func (w *StatusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// RouteUnmatched adalah nilai route untuk permintaan yang tidak cocok pola mana pun (404/405); path asli tidak
// dipakai agar label metrik dan log berkardinalitas rendah.
const RouteUnmatched = "unmatched"

// RouteOf returns the matched route template ("/bookings/{bookingId}"). Hanya valid setelah ServeMux
// memproses r, sehingga dibaca setelah next.ServeHTTP dan hanya bila request tidak disalin di antaranya.
func RouteOf(r *http.Request) string {
	if r.Pattern == "" {
		return RouteUnmatched
	}
	if _, path, ok := strings.Cut(r.Pattern, " "); ok {
		return path
	}
	return r.Pattern
}

// SkipObservation melaporkan path yang tidak dilog dan tidak dihitung (health check dan scrape metrik).
func SkipObservation(path string) bool { return path == "/health" || path == "/metrics" }

// AccessLog writes one line per request after it completes: method, route (template), status, duration_ms,
// bytes. requestId dan trace_id datang dari context (RequestID). Level: 5xx ERROR, 4xx WARN, lainnya INFO.
// Query string dan header tidak dicatat.
func AccessLog(logger *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if SkipObservation(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			start := time.Now()
			sw := WrapStatus(w)
			next.ServeHTTP(sw, r)

			level := slog.LevelInfo
			switch {
			case sw.Status >= http.StatusInternalServerError:
				level = slog.LevelError
			case sw.Status >= http.StatusBadRequest:
				level = slog.LevelWarn
			}
			logger.LogAttrs(r.Context(), level, "request",
				slog.String("method", r.Method), slog.String("route", RouteOf(r)), slog.Int("status", sw.Status),
				slog.Float64("duration_ms", float64(time.Since(start).Microseconds())/1000), slog.Int64("bytes", sw.Bytes))
		})
	}
}
