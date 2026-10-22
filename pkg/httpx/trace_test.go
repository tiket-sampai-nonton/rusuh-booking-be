package httpx

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	amznRoot   = "1-5759e988-bd862e3fe1be46a994272793"
	amznHeader = "Root=" + amznRoot + ";Parent=53995c3f42cd8ad8;Sampled=1"
	w3cTrace   = "4bf92f3577b34da6a3ce929d0e0e4736"
	w3cHeader  = "00-" + w3cTrace + "-00f067aa0ba902b7-01"
)

func hdr(kv ...string) http.Header {
	h := http.Header{}
	for i := 0; i < len(kv); i += 2 {
		h.Set(kv[i], kv[i+1])
	}
	return h
}

func TestTraceFromHeaders(t *testing.T) {
	tests := []struct {
		name    string
		h       http.Header
		want    string // "" = harus dibuat baru (32 hex)
		wantSrc string
	}{
		{"amzn root", hdr("X-Amzn-Trace-Id", amznHeader), amznRoot, "X-Amzn-Trace-Id"},
		{"w3c traceparent", hdr("Traceparent", w3cHeader), w3cTrace, "Traceparent"},
		{"amzn menang atas traceparent", hdr("X-Amzn-Trace-Id", amznHeader, "Traceparent", w3cHeader), amznRoot, "X-Amzn-Trace-Id"},
		{"tanpa header", hdr(), "", ""},
		{"amzn tanpa Root", hdr("X-Amzn-Trace-Id", "Parent=abc"), "", ""},
		{"amzn karakter aneh", hdr("X-Amzn-Trace-Id", "Root=1-abc\"; drop table"), "", ""},
		{"amzn terlalu panjang", hdr("X-Amzn-Trace-Id", "Root="+strings.Repeat("a", 200)), "", ""},
		{"traceparent id nol", hdr("Traceparent", "00-00000000000000000000000000000000-00f067aa0ba902b7-01"), "", ""},
		{"traceparent salah panjang", hdr("Traceparent", "00-abc-00f067aa0ba902b7-01"), "", ""},
		{"traceparent huruf besar", hdr("Traceparent", "00-4BF92F3577B34DA6A3CE929D0E0E4736-00f067aa0ba902b7-01"), "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := traceFromHeaders(tt.h)
			if tt.want == "" {
				assert.Regexp(t, `^[0-9a-f]{32}$`, got.id)
				assert.Empty(t, got.header, "id buatan sendiri tidak berasal dari header")
				return
			}
			assert.Equal(t, tt.want, got.id)
			assert.Equal(t, tt.wantSrc, got.header)
		})
	}
}

func TestRequestID_StoresTraceInContext(t *testing.T) {
	var seen string
	h := Chain(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { seen = TraceIDFrom(r.Context()) }), RequestID())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Amzn-Trace-Id", amznHeader)
	h.ServeHTTP(httptest.NewRecorder(), req)
	assert.Equal(t, amznRoot, seen)

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	assert.Regexp(t, `^[0-9a-f]{32}$`, seen)
}

func TestPropagate(t *testing.T) {
	run := func(in http.Header) *http.Request {
		var out *http.Request
		h := Chain(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			out, _ = http.NewRequestWithContext(r.Context(), http.MethodGet, "http://internal/x", nil)
			Propagate(r.Context(), out)
		}), RequestID())
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header = in
		h.ServeHTTP(httptest.NewRecorder(), req)
		require.NotNil(t, out)
		return out
	}

	t.Run("amzn: trace id sama, Parent pemanggil tidak ikut", func(t *testing.T) {
		out := run(hdr("X-Request-ID", "req-9", "X-Amzn-Trace-Id", amznHeader))
		assert.Equal(t, "req-9", out.Header.Get(HeaderRequestID))
		assert.Equal(t, "Root="+amznRoot, out.Header.Get("X-Amzn-Trace-Id"))
		assert.Empty(t, out.Header.Get("Traceparent"))
	})
	t.Run("traceparent: trace id sama, span baru", func(t *testing.T) {
		out := run(hdr("Traceparent", w3cHeader))
		tp := out.Header.Get("Traceparent")
		assert.Regexp(t, `^00-`+w3cTrace+`-[0-9a-f]{16}-01$`, tp)
		assert.NotContains(t, tp, "00f067aa0ba902b7")
		assert.NotEmpty(t, out.Header.Get(HeaderRequestID))
	})
	t.Run("id buatan sendiri dikirim sebagai traceparent", func(t *testing.T) {
		out := run(hdr())
		tp := out.Header.Get("Traceparent")
		assert.Regexp(t, `^00-[0-9a-f]{32}-[0-9a-f]{16}-01$`, tp)
	})
	t.Run("tanpa context request tidak panik", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, "http://x", nil)
		Propagate(req.Context(), req)
		assert.Empty(t, req.Header.Get(HeaderRequestID))
	})
}
