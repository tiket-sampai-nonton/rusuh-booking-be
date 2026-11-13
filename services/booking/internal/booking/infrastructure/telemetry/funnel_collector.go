// Package telemetry exposes Booking's business metrics (NFR-09, US-081) as a Prometheus collector.
package telemetry

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

// scrapeTimeout bounds the funnel queries so a slow database never stalls the whole /metrics response.
const scrapeTimeout = 2 * time.Second

// funnelCacheTTL is how long one read of the funnel serves every scrape (D-C7.6): the counts are an operations view, so
// they may lag by up to this long, and a scrape costs at most one set of queries per TTL per instance.
const funnelCacheTTL = 30 * time.Second

// FunnelCollector reads the conversion funnel (admissions and bookings per event and status) at scrape time and
// derives the three rates ops watch per event, computed once here so Grafana and CloudWatch show the same numbers
// (CloudWatch metric math cannot divide per event): conversion = bookings PAID / admissions, missed turn = admissions
// ended UNCLAIMED / admissions, expired = bookings EXPIRED / bookings. A rate is omitted while its denominator is 0.
// Every instance reports the same rows, so dashboards aggregate with max, never sum. The read is cached for
// funnelCacheTTL, so frequent scrapes do not each run the funnel queries.
type FunnelCollector struct {
	stats      outbound.IFunnelStats
	now        func() time.Time
	mu         sync.Mutex
	last       *funnelRead // the most recent read, nil until the first scrape
	lastAt     time.Time
	admissions *prometheus.Desc
	bookings   *prometheus.Desc
	eventInfo  *prometheus.Desc
	conversion *prometheus.Desc
	missedTurn *prometheus.Desc
	expired    *prometheus.Desc
	up         *prometheus.Desc
}

// NewFunnelCollector accepts the funnel port and the clock that dates the cached read, and returns the concrete collector.
func NewFunnelCollector(stats outbound.IFunnelStats, now func() time.Time) *FunnelCollector {
	return &FunnelCollector{
		stats: stats,
		now:   now,
		admissions: prometheus.NewDesc("rbt_booking_admissions",
			"Admission (sesi detail) per event, status, dan alasan akhir; dibaca dari PostgreSQL saat scrape.",
			[]string{"event_id", "status", "reason"}, nil),
		bookings: prometheus.NewDesc("rbt_booking_bookings",
			"Booking per event dan status; dibaca dari PostgreSQL saat scrape.",
			[]string{"event_id", "status"}, nil),
		eventInfo: prometheus.NewDesc("rbt_booking_event_info",
			"Nama event per event_id (nilai selalu 1); dipakai dashboard untuk menampilkan nama, bukan UUID.",
			[]string{"event_id", "event_name"}, nil),
		conversion: prometheus.NewDesc("rbt_booking_conversion_percent",
			"Booking PAID dibanding admission per event (persen).", []string{"event_id"}, nil),
		missedTurn: prometheus.NewDesc("rbt_booking_missed_turn_percent",
			"Admission yang berakhir UNCLAIMED dibanding semua admission per event (persen).", []string{"event_id"}, nil),
		expired: prometheus.NewDesc("rbt_booking_expired_percent",
			"Booking EXPIRED dibanding semua booking per event (persen).", []string{"event_id"}, nil),
		up: prometheus.NewDesc("rbt_booking_funnel_up",
			"1 bila metrik funnel terbaca dari PostgreSQL pada scrape ini, 0 bila gagal.", nil, nil),
	}
}

// Describe implements prometheus.Collector.
func (c *FunnelCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.admissions
	ch <- c.bookings
	ch <- c.eventInfo
	ch <- c.conversion
	ch <- c.missedTurn
	ch <- c.expired
	ch <- c.up
}

// eventTotals are the per-event sums behind the three rates.
type eventTotals struct{ admissions, unclaimed, bookings, paid, expired int64 }

// funnelRead is one read of the funnel: the rows behind every metric of a scrape. A failed read is kept too, so an outage
// costs one query per TTL rather than one per scrape.
type funnelRead struct {
	admissions []outbound.FunnelCount
	bookings   []outbound.FunnelCount
	names      []outbound.EventName
	err        error
}

// load returns the funnel read no more than funnelCacheTTL ago, and reads PostgreSQL again only when that read is older.
// The lock also makes concurrent scrapes wait for the one read in flight instead of starting their own.
func (c *FunnelCollector) load() funnelRead {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if c.last != nil && now.Sub(c.lastAt) < funnelCacheTTL {
		return *c.last
	}
	ctx, cancel := context.WithTimeout(context.Background(), scrapeTimeout)
	defer cancel()
	var r funnelRead
	r.admissions, r.err = c.stats.AdmissionCounts(ctx)
	if r.err == nil {
		r.bookings, r.err = c.stats.BookingCounts(ctx)
	}
	if r.err == nil {
		r.names, r.err = c.stats.EventNames(ctx)
	}
	if r.err != nil {
		slog.Warn("metrik funnel gagal dibaca", "error", r.err)
	}
	c.last, c.lastAt = &r, now
	return r
}

// Collect implements prometheus.Collector. A read failure reports up=0 instead of an error: an error would fail the
// whole /metrics response and hide the HTTP metrics too.
func (c *FunnelCollector) Collect(ch chan<- prometheus.Metric) {
	r := c.load()
	if r.err != nil {
		ch <- prometheus.MustNewConstMetric(c.up, prometheus.GaugeValue, 0)
		return
	}
	for _, row := range r.admissions {
		ch <- prometheus.MustNewConstMetric(c.admissions, prometheus.GaugeValue, float64(row.Count), row.EventID, row.Status, row.EndReason)
	}
	for _, row := range r.bookings {
		ch <- prometheus.MustNewConstMetric(c.bookings, prometheus.GaugeValue, float64(row.Count), row.EventID, row.Status)
	}
	for _, n := range r.names {
		ch <- prometheus.MustNewConstMetric(c.eventInfo, prometheus.GaugeValue, 1, n.EventID, n.Name)
	}
	totals := tally(r.admissions, r.bookings)
	for id, t := range totals {
		if t.admissions > 0 {
			ch <- prometheus.MustNewConstMetric(c.conversion, prometheus.GaugeValue, percent(t.paid, t.admissions), id)
			ch <- prometheus.MustNewConstMetric(c.missedTurn, prometheus.GaugeValue, percent(t.unclaimed, t.admissions), id)
		}
		if t.bookings > 0 {
			ch <- prometheus.MustNewConstMetric(c.expired, prometheus.GaugeValue, percent(t.expired, t.bookings), id)
		}
	}
	ch <- prometheus.MustNewConstMetric(c.up, prometheus.GaugeValue, 1)
}

// tally sums the rows per event into the numerators and denominators of the three rates.
func tally(adm, bk []outbound.FunnelCount) map[string]*eventTotals {
	totals := map[string]*eventTotals{}
	of := func(id string) *eventTotals {
		if totals[id] == nil {
			totals[id] = &eventTotals{}
		}
		return totals[id]
	}
	for _, r := range adm {
		t := of(r.EventID)
		t.admissions += r.Count
		if r.EndReason == "UNCLAIMED" {
			t.unclaimed += r.Count
		}
	}
	for _, r := range bk {
		t := of(r.EventID)
		t.bookings += r.Count
		switch r.Status {
		case "PAID":
			t.paid += r.Count
		case "EXPIRED":
			t.expired += r.Count
		}
	}
	return totals
}

func percent(part, whole int64) float64 { return 100 * float64(part) / float64(whole) }
