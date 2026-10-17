package delivery

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/config"
)

func TestWiringServesHealthAndReadiness(t *testing.T) {
	cfg := config.Config{
		ServiceName:  "booking",
		DialTimeout:  100 * time.Millisecond,
		Dependencies: []config.Dependency{{Name: "postgres", Addr: "127.0.0.1:1"}},
	}
	h, err := newHTTPHandler(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}

	for path, want := range map[string]int{"/healthz": http.StatusOK, "/readyz": http.StatusServiceUnavailable} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != want || rec.Header().Get("X-Request-ID") == "" {
			t.Errorf("%s: status %d (want %d), request id %q", path, rec.Code, want, rec.Header().Get("X-Request-ID"))
		}
	}
}

func TestWiringRejectsInvalidDependencyName(t *testing.T) {
	cfg := config.Config{Dependencies: []config.Dependency{{Name: "Bad Name"}}}

	if _, err := newHTTPHandler(cfg, slog.New(slog.NewTextHandler(io.Discard, nil))); err == nil {
		t.Fatal("expected error for invalid dependency name")
	}
}
