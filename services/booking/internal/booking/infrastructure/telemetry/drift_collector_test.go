package telemetry_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/infrastructure/telemetry"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

// countingDrift counts the reads of the drift query, so a test can see how many queries a run of scrapes made.
type countingDrift struct {
	rows   []outbound.StockDrift
	err    error
	reads  int
	budget time.Duration // time left on the context of the most recent read
}

func (f *countingDrift) StockDrift(ctx context.Context) ([]outbound.StockDrift, error) {
	f.reads++
	if dl, ok := ctx.Deadline(); ok {
		f.budget = time.Until(dl)
	}
	return f.rows, f.err
}

// The drift query scans booking_items and admissions in full, so it gets a 15 s budget of its own, not the 2 s funnel
// budget that keeps the other scrapes short.
func TestDriftCollector_ReadHasFullScanBudget(t *testing.T) {
	src := &countingDrift{}
	c := telemetry.NewDriftCollector(src, time.Now)
	require.Positive(t, testutil.CollectAndCount(c))
	require.Equal(t, 1, src.reads)
	require.InDelta(t, 15.0, src.budget.Seconds(), 1.0, "the drift read must get 15 s, not the 2 s funnel budget")
}

// The metric sums the absolute deltas per kind over all events, and every kind is exported even at 0, so an alert on
// the value sees a measured zero rather than a missing series.
func TestDriftCollector_SumsAbsoluteDeltaPerKind(t *testing.T) {
	src := &countingDrift{rows: []outbound.StockDrift{
		{EventID: "e1", Kind: "held", Delta: 1},
		{EventID: "e2", Kind: "held", Delta: -2},
		{EventID: "e1", Kind: "quota_held", Delta: 3},
	}}
	c := telemetry.NewDriftCollector(src, time.Now)
	want := `
# HELP rbt_booking_stock_drift Stok yang tidak cocok dengan hitung ulang dari baris hidup, Σ |selisih| per jenis (0 = tidak ada drift).
# TYPE rbt_booking_stock_drift gauge
rbt_booking_stock_drift{kind="allocated"} 0
rbt_booking_stock_drift{kind="category_held"} 0
rbt_booking_stock_drift{kind="category_sold"} 0
rbt_booking_stock_drift{kind="held"} 3
rbt_booking_stock_drift{kind="quota_held"} 3
rbt_booking_stock_drift{kind="sold"} 0
# HELP rbt_booking_stock_drift_up 1 bila drift stok terbaca dari PostgreSQL pada scrape ini, 0 bila gagal.
# TYPE rbt_booking_stock_drift_up gauge
rbt_booking_stock_drift_up 1
`
	require.NoError(t, testutil.CollectAndCompare(c, strings.NewReader(want)))
}

// A failing read reports up=0 and no drift values: a zero here would look like a clean stock.
func TestDriftCollector_ReadFailureReportsDown(t *testing.T) {
	c := telemetry.NewDriftCollector(&countingDrift{err: errors.New("db down")}, time.Now)
	want := `
# HELP rbt_booking_stock_drift_up 1 bila drift stok terbaca dari PostgreSQL pada scrape ini, 0 bila gagal.
# TYPE rbt_booking_stock_drift_up gauge
rbt_booking_stock_drift_up 0
`
	require.NoError(t, testutil.CollectAndCompare(c, strings.NewReader(want)))
}

// C9.1: the drift query runs at most once per 60 s per instance, so frequent scrapes share one read.
func TestDriftCollector_CachesFor60s(t *testing.T) {
	base := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	now := base
	src := &countingDrift{}
	c := telemetry.NewDriftCollector(src, func() time.Time { return now })

	require.Positive(t, testutil.CollectAndCount(c))
	now = base.Add(10 * time.Second)
	require.Positive(t, testutil.CollectAndCount(c))
	now = base.Add(59 * time.Second)
	require.Positive(t, testutil.CollectAndCount(c))
	require.Equal(t, 1, src.reads, "scrapes within 60 s must share one read of the drift query")

	now = base.Add(61 * time.Second)
	require.Positive(t, testutil.CollectAndCount(c))
	require.Equal(t, 2, src.reads, "a scrape after 60 s must read the drift query again")
}
