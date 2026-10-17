package httpx

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/apperror"
)

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestRequestIDReusesOrGenerates(t *testing.T) {
	var seen string
	h := Chain(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = RequestIDFrom(r.Context())
	}), RequestID())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(HeaderRequestID, "abc")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if seen != "abc" || rec.Header().Get(HeaderRequestID) != "abc" {
		t.Fatalf("incoming id not reused: ctx=%q header=%q", seen, rec.Header().Get(HeaderRequestID))
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if len(seen) != 32 {
		t.Fatalf("generated id = %q, want 32 hex chars", seen)
	}
}

func TestRecoverWritesEnvelope(t *testing.T) {
	h := Chain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") }),
		RequestID(), Recover(discard()))
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	var env apperror.Envelope
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatal(err)
	}
	if env.Error.Code != apperror.CodeInternal || env.Error.RequestID == "" {
		t.Fatalf("envelope = %+v", env)
	}
}

func TestServeShutsDownOnCancel(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer(ln.Addr().String(), http.NotFoundHandler())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, srv, ln, time.Second, discard()) }()

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve() error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server did not shut down")
	}
}
