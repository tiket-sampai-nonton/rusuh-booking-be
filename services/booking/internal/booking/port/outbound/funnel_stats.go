package outbound

import "context"

//go:generate go tool mockgen -source=funnel_stats.go -destination=../../../../_mock/booking/port/outbound/funnel_stats_mock.go -package=mock_outbound

// FunnelCount is one group of the conversion funnel: the number of rows per event and status (NFR-09).
type FunnelCount struct {
	EventID   string
	Status    string
	EndReason string // admissions only; "" while the admission is still active
	Count     int64
}

// IFunnelStats reads the conversion funnel from the store of record when metrics are scraped (NFR-09, US-081).
// Counting rows, not process counters, keeps the numbers right across restarts and multiple instances.
type IFunnelStats interface {
	AdmissionCounts(ctx context.Context) ([]FunnelCount, error)
	BookingCounts(ctx context.Context) ([]FunnelCount, error)
}
