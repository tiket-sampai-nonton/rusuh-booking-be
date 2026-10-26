// Package telemetry exposes Prometheus metrics (platform R4.1-R4.3, R4.6; ADR-023). Label dibatasi ke
// service, method, route (template), status, dan name; ID bisnis, NIK, dan email tidak pernah menjadi label.
package telemetry

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/httpx"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/resilience"
)

// Registry holds this process's metrics plus the standard Go and process collectors.
type Registry struct {
	reg      *prometheus.Registry
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
	breaker  *prometheus.GaugeVec
}

// Default is the registry used by the package-level helpers (satu per proses).
var Default = New()

// New creates a registry with the shared RBT metrics registered.
func New() *Registry {
	r := &Registry{
		reg: prometheus.NewRegistry(),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "rbt_http_requests_total", Help: "Jumlah request HTTP per route (template), method, dan status.",
		}, []string{"service", "method", "route", "status"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "rbt_http_request_duration_seconds", Help: "Durasi request HTTP per route (template).",
			Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
		}, []string{"service", "method", "route"}),
		breaker: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "breaker_state", Help: "State circuit breaker: 0 closed, 1 open, 2 half_open.",
		}, []string{"name"}),
	}
	r.reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		r.requests, r.duration, r.breaker)
	return r
}

// Handler serves the metrics in Prometheus text format (GET /metrics).
func (r *Registry) Handler() http.Handler {
	return promhttp.HandlerFor(r.reg, promhttp.HandlerOpts{})
}

// knownMethods membatasi nilai label method; method lain menjadi OTHER agar klien tidak bisa menambah seri.
var knownMethods = map[string]bool{
	http.MethodGet: true, http.MethodHead: true, http.MethodPost: true, http.MethodPut: true,
	http.MethodPatch: true, http.MethodDelete: true, http.MethodOptions: true,
}

// HTTP records request count and duration per route template. Pasang di dalam httpx.RequestID dan tanpa
// middleware lain yang menyalin request di antaranya dan mux, supaya r.Pattern terbaca (httpx.RouteOf).
// /health dan /metrics tidak dihitung.
func (r *Registry) HTTP(service string) httpx.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if httpx.SkipObservation(req.URL.Path) {
				next.ServeHTTP(w, req)
				return
			}
			start := time.Now()
			sw := httpx.WrapStatus(w)
			next.ServeHTTP(sw, req)

			method := req.Method
			if !knownMethods[method] {
				method = "OTHER"
			}
			route := httpx.RouteOf(req)
			r.requests.WithLabelValues(service, method, route, strconv.Itoa(sw.Status)).Inc()
			r.duration.WithLabelValues(service, method, route).Observe(time.Since(start).Seconds())
		})
	}
}

// BreakerObserver returns an OnStateChange callback for resilience.BreakerConfig that sets breaker_state{name}.
func (r *Registry) BreakerObserver() func(name string, from, to resilience.State) {
	return func(name string, _, to resilience.State) {
		r.breaker.WithLabelValues(name).Set(float64(to))
	}
}

// NewCounterVec registers a business counter in this registry. Labels must have a small, fixed set of values
// (method, status, result); never a booking id, NIK, or email. It panics on a duplicate name, like prometheus.MustRegister.
func (r *Registry) NewCounterVec(name, help string, labels ...string) *prometheus.CounterVec {
	c := prometheus.NewCounterVec(prometheus.CounterOpts{Name: name, Help: help}, labels)
	r.reg.MustRegister(c)
	return c
}

// NewCounterVec is Default.NewCounterVec.
func NewCounterVec(name, help string, labels ...string) *prometheus.CounterVec {
	return Default.NewCounterVec(name, help, labels...)
}

// Handler is Default.Handler.
func Handler() http.Handler { return Default.Handler() }

// HTTP is Default.HTTP.
func HTTP(service string) httpx.Middleware { return Default.HTTP(service) }

// BreakerObserver is Default.BreakerObserver.
func BreakerObserver() func(name string, from, to resilience.State) { return Default.BreakerObserver() }
