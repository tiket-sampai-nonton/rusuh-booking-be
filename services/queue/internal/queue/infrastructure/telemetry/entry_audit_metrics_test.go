package telemetry

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"

	pkgtelemetry "github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/telemetry"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

func TestEntryAuditMetrics_CountsDroppedRowsPerReason(t *testing.T) {
	m := NewEntryAuditMetrics(pkgtelemetry.New())

	m.Dropped(outbound.AuditDropBufferFull, 3)
	m.Dropped(outbound.AuditDropBufferFull, 2)
	m.Dropped(outbound.AuditDropClosed, 1)

	assert.Equal(t, 5.0, testutil.ToFloat64(m.dropped.WithLabelValues(outbound.AuditDropBufferFull)))
	assert.Equal(t, 1.0, testutil.ToFloat64(m.dropped.WithLabelValues(outbound.AuditDropClosed)))
	assert.Equal(t, 0.0, testutil.ToFloat64(m.dropped.WithLabelValues(outbound.AuditDropWriteFailed)))
}
