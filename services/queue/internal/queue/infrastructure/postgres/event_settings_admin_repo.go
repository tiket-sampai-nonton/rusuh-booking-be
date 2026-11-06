package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	pkgpg "github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/postgres"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// EventSettingsAdmin implements outbound.IEventSettingsAdminRepo: the only writer of event_settings (QSA-6.3).
type EventSettingsAdmin struct{ pool *pgxpool.Pool }

// NewEventSettingsAdmin returns the repository.
func NewEventSettingsAdmin(pool *pgxpool.Pool) *EventSettingsAdmin {
	return &EventSettingsAdmin{pool: pool}
}

// Apply runs read -> Decide -> write row -> append audit in one transaction. A per-event advisory lock serializes
// concurrent PUTs, which also covers the "row absent" case where FOR UPDATE has nothing to lock. READ COMMITTED is
// enough because every writer takes the lock first, so the in-transaction read is the latest committed state.
func (r *EventSettingsAdmin) Apply(ctx context.Context, p outbound.ApplySettingsParams) (outbound.ApplySettingsResult, error) {
	var res outbound.ApplySettingsResult
	err := pkgpg.WithTx(ctx, r.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('queue.event_settings:' || $1::text, 0))`, p.EventID); err != nil {
			return fmt.Errorf("event_settings: lock: %w", err)
		}
		cur, exists, err := lockedGet(ctx, tx, p.EventID)
		if err != nil {
			return err
		}
		merged, err := p.Decide(cur, exists)
		if err != nil {
			return err
		}
		merged.EventID = p.EventID
		changes := entity.DiffSettings(cur, exists, merged)
		if exists && len(changes) == 0 {
			res = outbound.ApplySettingsResult{Settings: cur}
			return nil
		}
		merged.UpdatedAt = p.Now
		if err := writeSettings(ctx, tx, merged, exists); err != nil {
			return err
		}
		if err := appendAudit(ctx, tx, p, changes); err != nil {
			return err
		}
		res = outbound.ApplySettingsResult{Settings: merged, Created: !exists, Changed: true}
		return nil
	})
	if err != nil {
		return outbound.ApplySettingsResult{}, err
	}
	return res, nil
}

func lockedGet(ctx context.Context, tx pgx.Tx, eventID string) (entity.EventSettings, bool, error) {
	s, err := scanEventSettings(tx.QueryRow(ctx, `SELECT `+eventSettingsCols+` FROM event_settings WHERE event_id = $1::uuid`, eventID))
	if errors.Is(err, pgx.ErrNoRows) {
		return entity.EventSettings{EventID: eventID}, false, nil
	}
	if err != nil {
		return entity.EventSettings{}, false, fmt.Errorf("event_settings: read: %w", err)
	}
	return s, true, nil
}

func writeSettings(ctx context.Context, tx pgx.Tx, s entity.EventSettings, exists bool) error {
	if exists {
		_, err := tx.Exec(ctx, `UPDATE event_settings SET active_slot_capacity=$2, near_front_multiplier=$3,
			status_publish_interval_ms=$4, poll_far_ms=$5, poll_mid_ms=$6, poll_near_ms=$7, one_active_per_visitor=$8,
			one_active_per_email=$9, category_limited_threshold_pct=$10, pre_turn_notify_enabled=$11, updated_at=$12
			WHERE event_id=$1::uuid`,
			s.EventID, s.ActiveSlotCapacity, s.NearFrontMultiplier, s.StatusPublishIntervalMs, s.PollFarMs, s.PollMidMs,
			s.PollNearMs, s.OneActivePerVisitor, s.OneActivePerEmail, s.CategoryLimitedThresholdPct,
			s.PreTurnNotifyEnabled, s.UpdatedAt)
		if err != nil {
			return fmt.Errorf("event_settings: update: %w", err)
		}
		return nil
	}
	_, err := tx.Exec(ctx, `INSERT INTO event_settings (event_id, active_slot_capacity, near_front_multiplier,
		status_publish_interval_ms, poll_far_ms, poll_mid_ms, poll_near_ms, one_active_per_visitor, one_active_per_email,
		category_limited_threshold_pct, pre_turn_notify_enabled, updated_at)
		VALUES ($1::uuid,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		s.EventID, s.ActiveSlotCapacity, s.NearFrontMultiplier, s.StatusPublishIntervalMs, s.PollFarMs, s.PollMidMs,
		s.PollNearMs, s.OneActivePerVisitor, s.OneActivePerEmail, s.CategoryLimitedThresholdPct,
		s.PreTurnNotifyEnabled, s.UpdatedAt)
	if err != nil {
		return fmt.Errorf("event_settings: insert: %w", err)
	}
	return nil
}

// appendAudit writes one row per changed field in the caller's transaction (private on purpose: QSA-4.6).
func appendAudit(ctx context.Context, tx pgx.Tx, p outbound.ApplySettingsParams, changes []entity.SettingChange) error {
	batch := &pgx.Batch{}
	for _, c := range changes {
		batch.Queue(`INSERT INTO event_settings_audit (event_id, actor, field, old_value, new_value, changed_at)
			VALUES ($1::uuid,$2,$3,$4,$5,$6)`, p.EventID, p.Actor, c.Field, c.Old, c.New, p.Now)
	}
	br := tx.SendBatch(ctx, batch)
	for range changes {
		if _, err := br.Exec(); err != nil {
			_ = br.Close()
			return fmt.Errorf("event_settings: audit: %w", err)
		}
	}
	if err := br.Close(); err != nil {
		return fmt.Errorf("event_settings: audit: %w", err)
	}
	return nil
}
