// Package postgres implements the Queue repositories on schema queue (pgx).
// PostgreSQL is the audit & recovery store, not the hot path (design §3.3).
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	pkgpg "github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/postgres"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
)

const eventSettingsCols = `event_id::text, active_slot_capacity, near_front_multiplier, status_publish_interval_ms,
	poll_far_ms, poll_mid_ms, poll_near_ms, one_active_per_visitor, one_active_per_email,
	category_limited_threshold_pct, pre_turn_notify_enabled, updated_at`

// EventSettings implements outbound.IEventSettingsRepo.
type EventSettings struct{ pool *pgxpool.Pool }

// NewEventSettings returns the repository.
func NewEventSettings(pool *pgxpool.Pool) *EventSettings { return &EventSettings{pool: pool} }

func scanEventSettings(row pgx.Row) (entity.EventSettings, error) {
	var s entity.EventSettings
	err := row.Scan(
		&s.EventID, &s.ActiveSlotCapacity, &s.NearFrontMultiplier, &s.StatusPublishIntervalMs,
		&s.PollFarMs, &s.PollMidMs, &s.PollNearMs, &s.OneActivePerVisitor, &s.OneActivePerEmail,
		&s.CategoryLimitedThresholdPct, &s.PreTurnNotifyEnabled, &s.UpdatedAt,
	)
	return s, err
}

// Get returns the per-event tuning. The bool is false (no error) when the event has no settings row.
func (r *EventSettings) Get(ctx context.Context, eventID string) (entity.EventSettings, bool, error) {
	s, err := scanEventSettings(r.pool.QueryRow(ctx, `SELECT `+eventSettingsCols+` FROM event_settings WHERE event_id = $1::uuid`, eventID))
	if errors.Is(err, pgx.ErrNoRows) || pkgpg.IsCode(err, "22P02") {
		return entity.EventSettings{}, false, nil
	}
	if err != nil {
		return entity.EventSettings{}, false, fmt.Errorf("event_settings: get: %w", err)
	}
	return s, true, nil
}

// Ensure inserts a row with the column defaults when missing, then returns the row.
func (r *EventSettings) Ensure(ctx context.Context, eventID string) (entity.EventSettings, error) {
	if _, err := r.pool.Exec(ctx, `INSERT INTO event_settings (event_id) VALUES ($1::uuid) ON CONFLICT (event_id) DO NOTHING`, eventID); err != nil {
		return entity.EventSettings{}, fmt.Errorf("event_settings: ensure: %w", err)
	}
	s, ok, err := r.Get(ctx, eventID)
	if err != nil || !ok {
		return entity.EventSettings{}, fmt.Errorf("event_settings: ensure: baris tidak terbaca: %w", err)
	}
	return s, nil
}

// ListEventIDs returns every configured event id (used by workers to iterate events).
func (r *EventSettings) ListEventIDs(ctx context.Context) ([]string, error) {
	rows, err := r.pool.Query(ctx, `SELECT event_id::text FROM event_settings`)
	if err != nil {
		return nil, fmt.Errorf("event_settings: list: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("event_settings: scan: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("event_settings: rows: %w", err)
	}
	return ids, nil
}
