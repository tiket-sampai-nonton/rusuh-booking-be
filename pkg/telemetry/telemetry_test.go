package telemetry

import (
	"net/http"
	"net/http/httptest"
	"testing"

	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/httpx"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/resilience"
)

func family(t *testing.T, r *Registry, name string) *dto.MetricFamily {
	t.Helper()
	fams, err := r.reg.Gather()
	require.NoError(t, err)
	for _, f := range fams {
		if f.GetName() == name {
			return f
		}
	}
	return nil
}

func labels(m *dto.Metric) map[string]string {
	out := map[string]string{}
	for _, l := range m.GetLabel() {
		out[l.GetName()] = l.GetValue()
	}
	return out
}

func serve(t *testing.T, r *Registry, method, path string) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /items/{id}", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	mux.HandleFunc("GET /health", func(http.ResponseWriter, *http.Request) {})
	mux.Handle("GET /metrics", r.Handler())
	h := httpx.Chain(mux, httpx.RequestID(), r.HTTP("svc"))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(method, path, nil))
}

func TestHTTP_CountsByRouteTemplate(t *testing.T) {
	r := New()
	serve(t, r, http.MethodGet, "/items/42")
	serve(t, r, http.MethodGet, "/items/43")

	f := family(t, r, "rbt_http_requests_total")
	require.NotNil(t, f)
	require.Len(t, f.GetMetric(), 1, "dua path berbeda -> satu seri (template)")
	m := f.GetMetric()[0]
	assert.Equal(t, map[string]string{"service": "svc", "method": "GET", "route": "/items/{id}", "status": "200"}, labels(m))
	assert.EqualValues(t, 2, m.GetCounter().GetValue())

	h := family(t, r, "rbt_http_request_duration_seconds")
	require.NotNil(t, h)
	assert.EqualValues(t, 2, h.GetMetric()[0].GetHistogram().GetSampleCount())
	assert.Equal(t, map[string]string{"service": "svc", "method": "GET", "route": "/items/{id}"}, labels(h.GetMetric()[0]))
}

func TestHTTP_UnmatchedAndUnknownMethod(t *testing.T) {
	r := New()
	serve(t, r, http.MethodGet, "/acak/abc-123")
	serve(t, r, "PURGE", "/items/1")

	got := map[string]bool{}
	for _, m := range family(t, r, "rbt_http_requests_total").GetMetric() {
		l := labels(m)
		got[l["method"]+" "+l["route"]+" "+l["status"]] = true
	}
	assert.True(t, got["GET unmatched 404"], got)
	assert.True(t, got["OTHER unmatched 405"], got, "405: mux tidak mengisi Pattern")
}

func TestHTTP_SkipsHealthAndMetrics(t *testing.T) {
	r := New()
	serve(t, r, http.MethodGet, "/health")
	serve(t, r, http.MethodGet, "/metrics")

	assert.Nil(t, family(t, r, "rbt_http_requests_total"))
}

func TestHandler_ExposesAppAndRuntimeMetrics(t *testing.T) {
	r := New()
	serve(t, r, http.MethodGet, "/items/1")
	rec := httptest.NewRecorder()

	r.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	body := rec.Body.String()
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, body, "rbt_http_requests_total")
	assert.Contains(t, body, "rbt_http_request_duration_seconds_bucket")
	assert.Contains(t, body, "go_goroutines")
	assert.Contains(t, body, "process_resident_memory_bytes")
}

func TestBreakerObserver(t *testing.T) {
	r := New()
	obs := r.BreakerObserver()

	obs("booking", resilience.Closed, resilience.Open)
	obs("smtp", resilience.Closed, resilience.HalfOpen)
	obs("booking", resilience.Open, resilience.HalfOpen)
	obs("booking", resilience.HalfOpen, resilience.Closed)
	obs("smtp", resilience.HalfOpen, resilience.Open)

	got := map[string]float64{}
	for _, m := range family(t, r, "breaker_state").GetMetric() {
		got[labels(m)["name"]] = m.GetGauge().GetValue()
	}
	assert.Equal(t, map[string]float64{"booking": 0, "smtp": 1}, got, "0 closed, 1 open, 2 half_open")
}

// R4.3: hanya label berkardinalitas rendah.
func TestLabelAllowlist(t *testing.T) {
	r := New()
	serve(t, r, http.MethodGet, "/items/1")
	r.BreakerObserver()("x", resilience.Closed, resilience.Open)

	allowed := map[string]bool{"service": true, "method": true, "route": true, "status": true, "name": true, "event_id": true, "reason": true, "le": true, "quantile": true}
	fams, err := r.reg.Gather()
	require.NoError(t, err)
	for _, f := range fams {
		if f.GetName() != "rbt_http_requests_total" && f.GetName() != "rbt_http_request_duration_seconds" && f.GetName() != "breaker_state" {
			continue
		}
		for _, m := range f.GetMetric() {
			for k := range labels(m) {
				assert.True(t, allowed[k], "%s memakai label %q di luar allowlist", f.GetName(), k)
			}
		}
	}
}
