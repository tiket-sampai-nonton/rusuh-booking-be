package outbound

import (
	"context"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
)

//go:generate go tool mockgen -source=repositories.go -destination=../../../../_mock/queue/port/outbound/repositories_mock.go -package=mock_outbound

// IEventSettingsRepo reads per-event tuning (Postgres event_settings).
type IEventSettingsRepo interface {
	Get(ctx context.Context, eventID string) (entity.EventSettings, bool, error)
	ListEventIDs(ctx context.Context) ([]string, error)
	// ListActiveEventIDs returns the events the workers iterate at now: every event whose sale ended less than a day
	// before now, and every event whose sale window is not known yet (D-C8.10).
	ListActiveEventIDs(ctx context.Context, now time.Time) ([]string, error)
	// UpdateSaleWindow stores the sale window Booking reported for eventID (D-C8.10).
	UpdateSaleWindow(ctx context.Context, eventID string, starts, ends time.Time) error
}

// ISettingsCacheInvalidator drops the cached settings of one event so the next read sees a committed change. A failed
// drop is not fatal: the cache TTL (30 s) bounds the staleness.
type ISettingsCacheInvalidator interface {
	Invalidate(ctx context.Context, eventID string) error
}

// IEntryAuditRepo appends to the append-only entry_audit table.
type IEntryAuditRepo interface {
	Append(ctx context.Context, e AuditRecord) error
}

// Why an audit row was not written (label reason of rbt_queue_entry_audit_dropped_total, D-C8.5).
const (
	AuditDropBufferFull  = "buffer_full"  // the writer's buffer was full when the row arrived
	AuditDropWriteFailed = "write_failed" // the batch insert failed
	AuditDropClosed      = "closed"       // the row arrived after shutdown began, or was still buffered at the deadline
)

// IEntryAuditMetrics counts the audit rows the best-effort writer did not store.
type IEntryAuditMetrics interface {
	Dropped(reason string, rows int)
}

// ApplySettingsParams is the input of IEventSettingsAdminRepo.Apply. Decide is the pure rule function
// (entity.Decide) re-run inside the transaction on the freshly locked row; Now stamps updated_at and the audit rows.
type ApplySettingsParams struct {
	EventID string
	Actor   string
	Now     time.Time
	Decide  entity.SettingsDecider
}

// ApplySettingsResult reports what Apply stored. Changed is false for an identical (no-op) request.
type ApplySettingsResult struct {
	Settings entity.EventSettings
	Created  bool
	Changed  bool
}

// IEventSettingsAdminRepo is the only write path of event_settings. Apply runs in one transaction under a per-event
// lock: read current, Decide, write the row and one append-only audit row per changed field (QSA-4.1). It exposes no
// audit update/delete operation by design (QSA-4.6).
type IEventSettingsAdminRepo interface {
	Apply(ctx context.Context, p ApplySettingsParams) (ApplySettingsResult, error)
}
