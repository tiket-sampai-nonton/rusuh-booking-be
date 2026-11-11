package outbound

import "context"

//go:generate go tool mockgen -source=depth.go -destination=../../../../_mock/queue/port/outbound/depth_mock.go -package=mock_outbound

// IQueueDepth is the read side of the queue store that the metrics collector needs (NFR-09). Every IQueueStore
// satisfies it.
type IQueueDepth interface {
	WaitingCount(ctx context.Context, eventID string) (int64, error)
	ActiveCount(ctx context.Context, eventID string) (int, error)
}

// IEventLister lists the events that have queue settings. IEventSettingsRepo satisfies it.
type IEventLister interface {
	ListEventIDs(ctx context.Context) ([]string, error)
}
