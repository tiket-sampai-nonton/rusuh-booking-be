package httpx

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
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
	if env.Meta.Code != 500 || env.Meta.Message != "INTERNAL_ERROR: terjadi kesalahan internal" || env.Meta.RequestID != "" {
		t.Fatalf("envelope = %+v", env)
	}
	if rec.Header().Get(HeaderRequestID) == "" {
		t.Fatal("requestId harus ada di header X-Request-ID, bukan di body")
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

func TestRequestID_RejectsUnsafeIncomingValue(t *testing.T) {
	h := Chain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), RequestID())
	for _, bad := range []string{`a"b`, "spasi di sini", "{\"x\":1}", "tab\tid", "é", strings.Repeat("a", 129)} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set(HeaderRequestID, bad)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if got := rec.Header().Get(HeaderRequestID); got == bad || len(got) != 32 {
			t.Fatalf("id %q seharusnya diganti id baru, dapat %q", bad, got)
		}
	}
}

func corsReq(t *testing.T, origin string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	rec := httptest.NewRecorder()
	CORS()(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(rec, req)
	return rec
}

func TestCORS_Allowlist(t *testing.T) {
	t.Setenv("CORS_ALLOWED_ORIGINS", "http://localhost:3000, https://rusuh-booking.pages.dev")

	ok := corsReq(t, "http://localhost:3000")
	if ok.Header().Get("Access-Control-Allow-Origin") != "http://localhost:3000" || ok.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatalf("allowed origin must be echoed with credentials: %v", ok.Header())
	}
	bad := corsReq(t, "https://evil.pages.dev")
	if bad.Header().Get("Access-Control-Allow-Origin") != "" || bad.Header().Get("Access-Control-Allow-Credentials") != "" {
		t.Fatalf("unlisted origin must get no CORS grant: %v", bad.Header())
	}
}

func TestCORS_EmptyListKeepsWildcardWithoutCredentials(t *testing.T) {
	t.Setenv("CORS_ALLOWED_ORIGINS", "")
	rec := corsReq(t, "http://localhost:3000")
	if rec.Header().Get("Access-Control-Allow-Origin") != "*" || rec.Header().Get("Access-Control-Allow-Credentials") != "" {
		t.Fatalf("unexpected headers: %v", rec.Header())
	}
}

func TestCookieSameSite(t *testing.T) {
	if CookieSameSite() != http.SameSiteLaxMode {
		t.Fatal("default must be Lax")
	}
	t.Setenv("COOKIE_SAMESITE", "None")
	if CookieSameSite() != http.SameSiteNoneMode {
		t.Fatal("COOKIE_SAMESITE=none must give None")
	}
}
