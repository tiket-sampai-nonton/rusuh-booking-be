package httpx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"regexp"
	"strings"
)

const (
	headerAmznTrace   = "X-Amzn-Trace-Id"
	headerTraceparent = "Traceparent"
)

type traceKey struct{}

// trace adalah id korelasi satu permintaan lintas service. header menyimpan asalnya ("" bila dibuat di sini).
type trace struct{ id, header string }

var (
	amznRootRe  = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)
	traceIDRe   = regexp.MustCompile(`^[0-9a-f]{32}$`)
	traceFieldR = regexp.MustCompile(`^[0-9a-f]{2}$`)
	spanIDRe    = regexp.MustCompile(`^[0-9a-f]{16}$`)
)

// traceFromHeaders mengambil trace id dari X-Amzn-Trace-Id (Root=, ditambahkan ALB), lalu traceparent (W3C),
// atau membuat yang baru. Nilai dari klien divalidasi ketat karena ditulis ke log.
func traceFromHeaders(h http.Header) trace {
	if id := parseAmznRoot(h.Get(headerAmznTrace)); id != "" {
		return trace{id: id, header: headerAmznTrace}
	}
	if id := w3cTraceID(h.Get(headerTraceparent)); id != "" {
		return trace{id: id, header: headerTraceparent}
	}
	return trace{id: newID()}
}

func parseAmznRoot(v string) string {
	for _, part := range strings.Split(v, ";") {
		if root, ok := strings.CutPrefix(strings.TrimSpace(part), "Root="); ok && amznRootRe.MatchString(root) {
			return root
		}
	}
	return ""
}

// w3cTraceID memvalidasi "00-<32 hex>-<16 hex>-<2 hex>" dan mengembalikan trace id-nya.
func w3cTraceID(v string) string {
	p := strings.Split(v, "-")
	if len(p) != 4 || !traceFieldR.MatchString(p[0]) || !traceIDRe.MatchString(p[1]) || !spanIDRe.MatchString(p[2]) || !traceFieldR.MatchString(p[3]) {
		return ""
	}
	if p[1] == strings.Repeat("0", 32) {
		return ""
	}
	return p[1]
}

// TraceIDFrom returns the trace id stored by the RequestID middleware.
func TraceIDFrom(ctx context.Context) string {
	t, _ := ctx.Value(traceKey{}).(trace)
	return t.id
}

// Propagate menyalin X-Request-ID dan trace id permintaan yang sedang diproses ke permintaan keluar ke
// service internal, sehingga satu permintaan bisa ditelusuri lintas service. Parent/span pemanggil tidak ikut.
func Propagate(ctx context.Context, req *http.Request) {
	if id := RequestIDFrom(ctx); id != "" {
		req.Header.Set(HeaderRequestID, id)
	}
	t, ok := ctx.Value(traceKey{}).(trace)
	if !ok {
		return
	}
	if t.header == headerAmznTrace {
		req.Header.Set(headerAmznTrace, "Root="+t.id)
		return
	}
	span := make([]byte, 8)
	_, _ = rand.Read(span)
	req.Header.Set(headerTraceparent, "00-"+t.id+"-"+hex.EncodeToString(span)+"-01")
}
