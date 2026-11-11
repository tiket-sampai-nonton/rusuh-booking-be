package telemetry

import (
	"context"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// depthScrapeTimeout bounds one scrape so a slow store never stalls the whole /metrics response.
const depthScrapeTimeout = 2 * time.Second

// DepthCollector reads queue depth per event from the queue store at scrape time (NFR-09, SDD §10.1:
// rbt_queue_waiting_count and rbt_queue_active_count). Every instance reads the same keys, so dashboards aggregate
// with max by (event_id), never sum.
type DepthCollector struct {
	events  outbound.IEventLister
	depth   outbound.IQueueDepth
	waiting *prometheus.Desc
	active  *prometheus.Desc
	up      *prometheus.Desc
}

// NewDepthCollector accepts the event lister and the depth reader and returns the concrete collector.
func NewDepthCollector(events outbound.IEventLister, depth outbound.IQueueDepth) *DepthCollector {
	return &DepthCollector{
		events: events,
		depth:  depth,
		waiting: prometheus.NewDesc("rbt_queue_waiting_count",
			"Entry WAITING yang masih hidup per event; dibaca dari Valkey saat scrape.", []string{"event_id"}, nil),
		active: prometheus.NewDesc("rbt_queue_active_count",
			"Slot terpakai per event (ADMITTED, CLAIMED, dan MISSED yang belum disapu), dibanding kapasitas oleh promotor; dibaca dari Valkey saat scrape.", []string{"event_id"}, nil),
		up: prometheus.NewDesc("rbt_queue_depth_up",
			"1 bila kedalaman antrean terbaca pada scrape ini, 0 bila gagal.", nil, nil),
	}
}

// Describe implements prometheus.Collector.
func (c *DepthCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.waiting
	ch <- c.active
	ch <- c.up
}

type eventDepth struct {
	id      string
	waiting int64
	active  int
}

// Collect implements prometheus.Collector. Any read failure reports up=0 and nothing else: an error would fail the
// whole /metrics response, and partial numbers would look like an emptied queue.
func (c *DepthCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), depthScrapeTimeout)
	defer cancel()
	rows, err := c.read(ctx)
	if err != nil {
		slog.Warn("metrik kedalaman antrean gagal dibaca", "error", err)
		ch <- prometheus.MustNewConstMetric(c.up, prometheus.GaugeValue, 0)
		return
	}
	for _, r := range rows {
		ch <- prometheus.MustNewConstMetric(c.waiting, prometheus.GaugeValue, float64(r.waiting), r.id)
		ch <- prometheus.MustNewConstMetric(c.active, prometheus.GaugeValue, float64(r.active), r.id)
	}
	ch <- prometheus.MustNewConstMetric(c.up, prometheus.GaugeValue, 1)
}

func (c *DepthCollector) read(ctx context.Context) ([]eventDepth, error) {
	ids, err := c.events.ListEventIDs(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]eventDepth, 0, len(ids))
	for _, id := range ids {
		w, err := c.depth.WaitingCount(ctx, id)
		if err != nil {
			return nil, err
		}
		a, err := c.depth.ActiveCount(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, eventDepth{id: id, waiting: w, active: a})
	}
	return out, nil
}
