package telemetry_test

import (
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pkgtelemetry "github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/telemetry"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/infrastructure/telemetry"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

func scrape(t *testing.T, reg *pkgtelemetry.Registry) string {
	t.Helper()
	rec := httptest.NewRecorder()
	reg.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	require.Equal(t, 200, rec.Code)
	return rec.Body.String()
}

var _ outbound.IPromoterMetrics = (*telemetry.PromoterMetrics)(nil)

// Every counter of design §5.4a.2 rule 5 is registered with the exact name and label set.
func TestPromoterMetrics_EveryCounterAppearsAfterOneInc(t *testing.T) {
	reg := pkgtelemetry.New()
	m := telemetry.NewPromoterMetrics(reg)
	m.Inc(outbound.MetricFullAfterGrant)
	m.Inc(outbound.MetricLeaseLost)
	m.Inc(outbound.MetricEntryEnded, "outcome", "ALREADY_ACTIVE")
	m.Inc(outbound.MetricEntryEnded, "outcome", "PROMOTER_ORPHAN")
	m.Inc(outbound.MetricEntryEnded, "outcome", "STALE_TIMEOUT")
	m.Inc(outbound.MetricLapsedReplay)
	m.Inc(outbound.MetricFifoAdopted, "suspect", "false")
	m.Inc(outbound.MetricFifoAdopted, "suspect", "true")
	m.Inc(outbound.MetricHeadNoSupply, "adopted", "true")
	m.Inc(outbound.MetricHeadNoSupply, "adopted", "false")
	m.Inc(outbound.MetricUnknownOutcome)
	m.Inc(outbound.MetricJoinLeaseRetry)

	out := scrape(t, reg)
	for _, want := range []string{
		"queue_promote_full_after_grant_total 1",
		"queue_promote_lease_lost_total 1",
		`queue_entry_ended_total{outcome="ALREADY_ACTIVE"} 1`,
		`queue_entry_ended_total{outcome="PROMOTER_ORPHAN"} 1`,
		`queue_entry_ended_total{outcome="STALE_TIMEOUT"} 1`,
		"queue_promote_lapsed_replay_skipped_total 1",
		`queue_promote_fifo_adopted_total{suspect="false"} 1`,
		`queue_promote_fifo_adopted_total{suspect="true"} 1`,
		`queue_promote_head_no_supply_repeat_total{adopted="true"} 1`,
		`queue_promote_head_no_supply_repeat_total{adopted="false"} 1`,
		"queue_promote_unknown_outcome_total 1",
		"queue_join_lease_retry_total 1",
	} {
		assert.Contains(t, out, want)
	}
}

// An unknown name or a label value outside the allowed set is dropped: it never panics and never creates a series
// (no unbounded label cardinality, nothing derived from user input).
func TestPromoterMetrics_UnknownNameOrLabelValueIsDroppedWithoutPanic(t *testing.T) {
	reg := pkgtelemetry.New()
	m := telemetry.NewPromoterMetrics(reg)
	assert.NotPanics(t, func() {
		m.Inc("queue_something_else_total")
		m.Inc(outbound.MetricEntryEnded, "outcome", "NIK-3201234567890123")
		m.Inc(outbound.MetricEntryEnded) // missing label
		m.Inc(outbound.MetricFifoAdopted, "wrong", "false")
		m.Inc(outbound.MetricLeaseLost, "unexpected", "label")
	})
	out := scrape(t, reg)
	assert.NotContains(t, out, "NIK-3201234567890123")
	assert.NotContains(t, out, "queue_something_else_total")
	assert.NotContains(t, out, `unexpected="label"`)
}
