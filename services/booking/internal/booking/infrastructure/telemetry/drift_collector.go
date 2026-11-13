package telemetry

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

// driftCacheTTL is how long one drift read serves every scrape (C9.1): the recount is a stock check, so it may lag by up
// to this long, and a scrape costs at most one drift query per TTL per instance.
const driftCacheTTL = 60 * time.Second

// driftKinds are the kinds of stock_drift.sql, always exported so that a zero is measured rather than absent.
var driftKinds = []string{"allocated", "held", "sold", "category_held", "category_sold", "quota_held"}

// DriftCollector exports rbt_booking_stock_drift{kind}: the sum of the absolute drift per kind over all events, read
// from PostgreSQL at scrape time and cached for driftCacheTTL. A failed read reports rbt_booking_stock_drift_up 0 and no
// drift values, so a failure never looks like a clean stock.
type DriftCollector struct {
	src    outbound.IStockDrift
	now    func() time.Time
	mu     sync.Mutex
	last   *driftRead // the most recent read, nil until the first scrape
	lastAt time.Time
	drift  *prometheus.Desc
	up     *prometheus.Desc
}

// driftRead is one read of the drift query. A failed read is kept too, so an outage costs one query per TTL.
type driftRead struct {
	rows []outbound.StockDrift
	err  error
}

// NewDriftCollector accepts the drift port and the clock that dates the cached read, and returns the concrete collector.
func NewDriftCollector(src outbound.IStockDrift, now func() time.Time) *DriftCollector {
	return &DriftCollector{
		src: src,
		now: now,
		drift: prometheus.NewDesc("rbt_booking_stock_drift",
			"Stok yang tidak cocok dengan hitung ulang dari baris hidup, Σ |selisih| per jenis (0 = tidak ada drift).",
			[]string{"kind"}, nil),
		up: prometheus.NewDesc("rbt_booking_stock_drift_up",
			"1 bila drift stok terbaca dari PostgreSQL pada scrape ini, 0 bila gagal.", nil, nil),
	}
}

// Describe implements prometheus.Collector.
func (c *DriftCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.drift
	ch <- c.up
}

// load returns the drift read no more than driftCacheTTL ago, and reads PostgreSQL again only when that read is older.
// The lock makes concurrent scrapes wait for the one read in flight.
func (c *DriftCollector) load() driftRead {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if c.last != nil && now.Sub(c.lastAt) < driftCacheTTL {
		return *c.last
	}
	ctx, cancel := context.WithTimeout(context.Background(), scrapeTimeout)
	defer cancel()
	rows, err := c.src.StockDrift(ctx)
	if err != nil {
		slog.Warn("metrik drift stok gagal dibaca", "error", err)
	}
	c.last, c.lastAt = &driftRead{rows: rows, err: err}, now
	return *c.last
}

// Collect implements prometheus.Collector.
func (c *DriftCollector) Collect(ch chan<- prometheus.Metric) {
	r := c.load()
	if r.err != nil {
		ch <- prometheus.MustNewConstMetric(c.up, prometheus.GaugeValue, 0)
		return
	}
	sums := make(map[string]int64, len(driftKinds))
	for _, d := range r.rows {
		sums[d.Kind] += absInt64(int64(d.Delta))
	}
	for _, kind := range driftKinds {
		ch <- prometheus.MustNewConstMetric(c.drift, prometheus.GaugeValue, float64(sums[kind]), kind)
	}
	ch <- prometheus.MustNewConstMetric(c.up, prometheus.GaugeValue, 1)
}

func absInt64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
