package outbound

import (
	"context"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
)

//go:generate go tool mockgen -source=repositories.go -destination=../../../../_mock/queue/port/outbound/repositories_mock.go -package=mock_outbound

// IEventSettingsRepo reads per-event tuning (Postgres event_settings).
type IEventSettingsRepo interface {
	Get(ctx context.Context, eventID string) (entity.EventSettings, bool, error)
	ListEventIDs(ctx context.Context) ([]string, error)
	// Ensure creates the settings row with column defaults when the event has none (idempotent) and returns it.
	Ensure(ctx context.Context, eventID string) (entity.EventSettings, error)
}

// IEntryAuditRepo appends to the append-only entry_audit table.
type IEntryAuditRepo interface {
	Append(ctx context.Context, e AuditRecord) error
}
