package telemetry_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/infrastructure/telemetry"
)

type fakeEvents struct {
	ids []string
	err error
}

func (f fakeEvents) ListEventIDs(context.Context) ([]string, error) { return f.ids, f.err }

type fakeDepth struct {
	waiting   map[string]int64
	active    map[string]int
	err       error
	activeErr error
}

func (f fakeDepth) WaitingCount(_ context.Context, id string) (int64, error) {
	return f.waiting[id], f.err
}
func (f fakeDepth) ActiveCount(_ context.Context, id string) (int, error) {
	if f.activeErr != nil {
		return 0, f.activeErr
	}
	return f.active[id], f.err
}

func TestDepthCollector_ExposesWaitingAndActivePerEvent(t *testing.T) {
	c := telemetry.NewDepthCollector(fakeEvents{ids: []string{"e1", "e2"}},
		fakeDepth{waiting: map[string]int64{"e1": 120, "e2": 0}, active: map[string]int{"e1": 50, "e2": 3}})
	want := `
# HELP rbt_queue_active_count Slot terpakai per event (ADMITTED, CLAIMED, dan MISSED yang belum disapu), dibanding kapasitas oleh promotor; dibaca dari Valkey saat scrape.
# TYPE rbt_queue_active_count gauge
rbt_queue_active_count{event_id="e1"} 50
rbt_queue_active_count{event_id="e2"} 3
# HELP rbt_queue_depth_up 1 bila kedalaman antrean terbaca pada scrape ini, 0 bila gagal.
# TYPE rbt_queue_depth_up gauge
rbt_queue_depth_up 1
# HELP rbt_queue_waiting_count Entry WAITING yang masih hidup per event; dibaca dari Valkey saat scrape.
# TYPE rbt_queue_waiting_count gauge
rbt_queue_waiting_count{event_id="e1"} 120
rbt_queue_waiting_count{event_id="e2"} 0
`
	require.NoError(t, testutil.CollectAndCompare(c, strings.NewReader(want)))
}

// A failing read must not break /metrics; it reports up=0 and no partial numbers.
func TestDepthCollector_ReadFailureReportsDown(t *testing.T) {
	want := `
# HELP rbt_queue_depth_up 1 bila kedalaman antrean terbaca pada scrape ini, 0 bila gagal.
# TYPE rbt_queue_depth_up gauge
rbt_queue_depth_up 0
`
	for name, c := range map[string]*telemetry.DepthCollector{
		"list":   telemetry.NewDepthCollector(fakeEvents{err: errors.New("pg down")}, fakeDepth{}),
		"count":  telemetry.NewDepthCollector(fakeEvents{ids: []string{"e1"}}, fakeDepth{err: errors.New("valkey down")}),
		"active": telemetry.NewDepthCollector(fakeEvents{ids: []string{"e1"}}, fakeDepth{activeErr: errors.New("valkey down")}),
	} {
		require.NoError(t, testutil.CollectAndCompare(c, strings.NewReader(want)), name)
	}
}
