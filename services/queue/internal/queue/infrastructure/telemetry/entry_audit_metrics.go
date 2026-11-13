package telemetry

import (
	"github.com/prometheus/client_golang/prometheus"

	pkgtelemetry "github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/telemetry"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// EntryAuditMetrics implements outbound.IEntryAuditMetrics: rbt_queue_entry_audit_dropped_total by reason (D-C8.5).
type EntryAuditMetrics struct {
	dropped *prometheus.CounterVec
}

// NewEntryAuditMetrics registers the dropped-rows counter on reg.
func NewEntryAuditMetrics(reg *pkgtelemetry.Registry) *EntryAuditMetrics {
	return &EntryAuditMetrics{dropped: reg.NewCounterVec("rbt_queue_entry_audit_dropped_total",
		"Baris entry_audit yang tidak tertulis (buffer penuh, batch gagal, atau setelah shutdown); best effort.", "reason")}
}

// Dropped adds rows to the counter of reason. The reasons are the outbound.AuditDrop* constants, a fixed set.
func (m *EntryAuditMetrics) Dropped(reason string, rows int) {
	m.dropped.WithLabelValues(reason).Add(float64(rows))
}

var _ outbound.IEntryAuditMetrics = (*EntryAuditMetrics)(nil)
