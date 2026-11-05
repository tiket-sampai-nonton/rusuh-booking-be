// Package telemetry adapts the promoter/join counters (design §5.4a.2 rule 5) to a pkg/telemetry.Registry.
package telemetry

import (
	"log/slog"

	"github.com/prometheus/client_golang/prometheus"

	pkgtelemetry "github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/telemetry"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// spec is the fixed shape of one counter: its label names, in the order Inc must receive them.
type spec struct {
	vec     *prometheus.CounterVec
	labels  []string
	allowed []map[string]bool // allowed values per label, same order as labels
}

func set(values ...string) map[string]bool {
	m := make(map[string]bool, len(values))
	for _, v := range values {
		m[v] = true
	}
	return m
}

// PromoterMetrics implements outbound.IPromoterMetrics. Every name and label set is fixed at construction time
// (design §5.4a.2 rule 5); Inc silently drops anything outside that set so a bug can never create an unbounded
// label cardinality or leak user data into a metric.
type PromoterMetrics struct {
	specs map[string]spec
}

// NewPromoterMetrics registers the exact counters of design §5.4a.2 rule 5 on reg.
func NewPromoterMetrics(reg *pkgtelemetry.Registry) *PromoterMetrics {
	m := &PromoterMetrics{specs: map[string]spec{}}
	add := func(name, help string, allowed []map[string]bool, labels ...string) {
		m.specs[name] = spec{vec: reg.NewCounterVec(name, help, labels...), labels: labels, allowed: allowed}
	}
	boolean := []map[string]bool{set("true", "false")}
	add(outbound.MetricFullAfterGrant, "Promote menjawab FULL setelah alokasi diberikan (seharusnya tak tercapai di bawah lease yang sah).", nil)
	add(outbound.MetricLeaseLost, "promote.lua menolak karena token lease tidak cocok lagi (fence).", nil)
	add(outbound.MetricEntryEnded, "Entry diakhiri promoter/sweeper per outcome.",
		[]map[string]bool{set("ALREADY_ACTIVE", "PROMOTER_ORPHAN", "SWEEP_MISSED", "STALE_TIMEOUT")}, "outcome")
	add(outbound.MetricLapsedReplay, "Replay GRANTED dengan deadline lampau dilewati tanpa mutasi.", nil)
	add(outbound.MetricFifoAdopted, "Grant di belakang NO_SUPPLY_NOW diadopsi (inversi FIFO terbatas).", boolean, "suspect")
	add(outbound.MetricHeadNoSupply, "Head menjawab NO_SUPPLY_NOW berturut-turut >= N tick (C8).", boolean, "adopted")
	add(outbound.MetricUnknownOutcome, "Outcome Booking yang tidak dikenal oleh promoter.", nil)
	add(outbound.MetricJoinLeaseRetry, "Join mencoba ulang akuisisi lease admisi (kontensi).", nil)
	return m
}

// Inc increments name with the given alternating label key/value pairs. A name outside the fixed set, a wrong
// number of labels, or a label key that does not match the registered set is dropped and logged once; it never
// panics.
func (m *PromoterMetrics) Inc(name string, labels ...string) {
	sp, ok := m.specs[name]
	if !ok {
		slog.Warn("promoter metric: nama tak dikenal, diabaikan", "name", name)
		return
	}
	if len(labels) != len(sp.labels)*2 {
		slog.Warn("promoter metric: jumlah label tidak cocok, diabaikan", "name", name)
		return
	}
	values := make([]string, len(sp.labels))
	for i, want := range sp.labels {
		key, val := labels[2*i], labels[2*i+1]
		if key != want {
			slog.Warn("promoter metric: kunci label tidak cocok, diabaikan", "name", name, "want", want, "got", key)
			return
		}
		if !sp.allowed[i][val] {
			slog.Warn("promoter metric: nilai label di luar himpunan yang diizinkan, diabaikan", "name", name, "label", want)
			return
		}
		values[i] = val
	}
	sp.vec.WithLabelValues(values...).Inc()
}
