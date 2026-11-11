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

// EventName is the display name of one event, exposed as an info metric so dashboards can show names, not UUIDs.
type EventName struct {
	EventID string
	Name    string
}

// IFunnelStats reads the conversion funnel from the store of record when metrics are scraped (NFR-09, US-081).
// Counting rows, not process counters, keeps the numbers right across restarts and multiple instances.
type IFunnelStats interface {
	AdmissionCounts(ctx context.Context) ([]FunnelCount, error)
	BookingCounts(ctx context.Context) ([]FunnelCount, error)
	// EventNames lists every event with its name. The set is bounded by the number of events, so it is safe as labels.
	EventNames(ctx context.Context) ([]EventName, error)
}
