package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	pkgpg "github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/postgres"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/entity"
	inbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
)

// EventWriteRepo creates and configures events (admin-only writes).
type EventWriteRepo struct{ pool *pgxpool.Pool }

// NewEventWriteRepo returns the write repository.
func NewEventWriteRepo(pool *pgxpool.Pool) *EventWriteRepo { return &EventWriteRepo{pool: pool} }

// CreateEvent inserts an event + event_supply + categories + inventories in one transaction.
// The deferred constraint check_supply_matches_categories fires at COMMIT, so a supply/category
// mismatch surfaces as a check violation when the transaction commits.
// Lock order for a brand-new event: supply → categories (no existing rows to lock).
func (r *EventWriteRepo) CreateEvent(ctx context.Context, id string, req dto.CreateEventRequest) error {
	err := pkgpg.WithTx(ctx, r.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO booking.events (
				id, slug, name, venue, starts_at, sales_starts_at, sales_ends_at, status,
				max_tickets_per_order, max_tickets_per_buyer_nik, allow_mixed_categories, description,
				city, genre)
			VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, 'DRAFT', $8, $9, $10, $11::jsonb, $12, $13)`,
			id, req.Slug, req.Name, req.Venue, req.StartsAt, req.SalesStartsAt, req.SalesEndsAt,
			req.MaxTicketsPerOrder, req.MaxTicketsPerBuyerNIK, req.AllowMixedCategories,
			descriptionArg(req.Description), req.City, genresParam(req.Genres)); err != nil {
			return err
		}

		var totalCapacity int
		for _, c := range req.Categories {
			totalCapacity += c.Capacity
		}

		// Supply first (lock order: supply → categories).
		if _, err := tx.Exec(ctx, `
			INSERT INTO booking.event_supply (event_id, total_capacity)
			VALUES ($1::uuid, $2)`, id, totalCapacity); err != nil {
			return err
		}

		for i, c := range req.Categories {
			var categoryID string
			if err := tx.QueryRow(ctx, `
				INSERT INTO booking.ticket_categories (event_id, code, name, price_minor, display_order)
				VALUES ($1::uuid, $2, $3, $4, $5)
				RETURNING id::text`,
				id, c.Code, c.Name, c.PriceMinor, i).Scan(&categoryID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO booking.category_inventories (category_id, event_id, capacity)
				VALUES ($1::uuid, $2::uuid, $3)`, categoryID, id, c.Capacity); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return mapEventWriteErr(err, "events: create")
	}
	return nil
}

// UpdateConfig applies mutable configuration changes to an event. Attempting to change a locked
// rule (after sales open) trips the guard_event_rules trigger, mapped to inbound.ErrRulesLocked.
// A cancelled event refuses the change (inbound.ErrEventCancelled). actor is written to config_audit.changed_by.
func (r *EventWriteRepo) UpdateConfig(ctx context.Context, eventID, actor string, req dto.UpdateEventConfigRequest) error {
	sql, args := buildUpdateConfig(eventID, req)
	return pkgpg.WithTx(ctx, r.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		status, err := lockEvent(ctx, tx, eventID)
		if err != nil {
			return err
		}
		if status == entity.EventCancelled {
			return inbound.ErrEventCancelled
		}
		if err := setActor(ctx, tx, actor); err != nil {
			return mapEventWriteErr(err, "events: update config")
		}
		if sql == "" {
			return nil // no-op: nothing to change, the event exists
		}
		tag, err := tx.Exec(ctx, sql, args...)
		if err != nil {
			return mapEventWriteErr(err, "events: update config")
		}
		if tag.RowsAffected() == 0 {
			return inbound.ErrNotFound
		}
		return nil
	})
}

// lockEvent takes the event row lock (SELECT ... FOR UPDATE) and returns its status, so the check and the write that
// follows in the same transaction see one status. An unknown event, or an id that is not a UUID, is ErrNotFound.
func lockEvent(ctx context.Context, tx pgx.Tx, eventID string) (entity.EventStatus, error) {
	var status string
	err := tx.QueryRow(ctx, `SELECT status FROM booking.events WHERE id = $1::uuid FOR UPDATE`, eventID).Scan(&status)
	switch {
	case errors.Is(err, pgx.ErrNoRows), pkgpg.IsCode(err, "22P02"):
		return "", inbound.ErrNotFound
	case err != nil:
		return "", mapEventWriteErr(err, "events: lock")
	}
	return entity.EventStatus(status), nil
}

// setActor records actor for the config_audit trigger of this transaction only (set_config with is_local = true).
func setActor(ctx context.Context, tx pgx.Tx, actor string) error {
	_, err := tx.Exec(ctx, `SELECT set_config('app.actor', $1, true)`, actor)
	return err
}

// buildUpdateConfig returns the UPDATE statement and its args ($1 is the event id) for the fields set in req.
// An empty statement means nothing to change.
func buildUpdateConfig(eventID string, req dto.UpdateEventConfigRequest) (string, []any) {
	sets := make([]string, 0, 7)
	args := []any{eventID}
	add := func(col string, v any) {
		args = append(args, v)
		sets = append(sets, fmt.Sprintf("%s = $%d", col, len(args)))
	}
	if req.SelectionTTLSeconds != nil {
		add("selection_ttl_seconds", *req.SelectionTTLSeconds)
	}
	if req.PaymentTTLSeconds != nil {
		add("payment_ttl_seconds", *req.PaymentTTLSeconds)
	}
	if req.EditEnabled != nil {
		add("selection_edit_enabled", *req.EditEnabled)
	}
	if req.MaxEdits != nil {
		add("max_selection_edits", *req.MaxEdits)
	}
	if req.City != nil { // "" clears the city
		add("city", *req.City)
	}
	if req.Genres != nil { // [] clears the genres; genresParam keeps the bound array non-NULL (column is NOT NULL)
		add("genre", genresParam(*req.Genres))
	}
	if len(req.Description) > 0 { // JSON null is present: it clears the column
		args = append(args, descriptionArg(req.Description))
		sets = append(sets, fmt.Sprintf("description = $%d::jsonb", len(args)))
	}
	if len(sets) == 0 {
		return "", nil
	}
	return fmt.Sprintf(`UPDATE booking.events SET %s WHERE id = $1::uuid`, strings.Join(sets, ", ")), args
}

// descriptionArg binds a Slate document as jsonb text; empty or JSON null becomes SQL NULL (no description).
func descriptionArg(raw json.RawMessage) any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return string(raw)
}

// mapEventWriteErr translates PostgreSQL errors raised by the event triggers/constraints into domain
// errors. The guard_event_rules trigger and check_supply_matches_categories both RAISE with ERRCODE
// check_violation, so they are distinguished by the message prefix.
func mapEventWriteErr(err error, op string) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "EVENT_RULES_LOCKED"):
		return inbound.ErrRulesLocked
	case strings.Contains(msg, "SUPPLY_CATEGORY_MISMATCH"):
		return inbound.ErrSupplyMismatch
	case pkgpg.IsCode(err, pkgpg.CodeUniqueViolation):
		return inbound.Validation("slug", "slug atau kode kategori sudah dipakai")
	case pkgpg.IsCode(err, "22P02"):
		return inbound.ErrNotFound
	case pkgpg.IsCode(err, pkgpg.CodeCheckViolation):
		// A value outside the table's limits (e.g. payment TTL below 120 s) is the caller's mistake, not a server fault.
		return inbound.Validation("config", "nilai di luar batas yang diizinkan ("+pkgpg.ConstraintName(err)+")")
	}
	return fmt.Errorf("%s: %w", op, err)
}

// imageColumns maps an upload kind to its events column (fixed set: never built from input).
var imageColumns = map[string]string{"image": "image_url", "banner": "banner_url", "thumbnail": "thumbnail_url"}

// SetImageURL stores the public URL of an uploaded image on the event.
func (r *EventWriteRepo) SetImageURL(ctx context.Context, eventID, kind, url string) error {
	col, ok := imageColumns[kind]
	if !ok {
		return inbound.Validation("kind", "harus image, banner, atau thumbnail")
	}
	tag, err := r.pool.Exec(ctx, "UPDATE booking.events SET "+col+" = $2 WHERE id = $1::uuid", eventID, url)
	if pkgpg.IsCode(err, "22P02") || (err == nil && tag.RowsAffected() == 0) {
		return inbound.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("events: set image url: %w", err)
	}
	return nil
}

// SetPublished publishes the event (published_at is kept when already published) or turns it back into a draft.
// A cancelled event refuses the change (inbound.ErrEventCancelled). actor is written to config_audit.changed_by.
func (r *EventWriteRepo) SetPublished(ctx context.Context, eventID, actor string, published bool) error {
	return pkgpg.WithTx(ctx, r.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		status, err := lockEvent(ctx, tx, eventID)
		if err != nil {
			return err
		}
		if status == entity.EventCancelled {
			return inbound.ErrEventCancelled
		}
		if err := setActor(ctx, tx, actor); err != nil {
			return mapEventWriteErr(err, "events: set published")
		}
		_, err = tx.Exec(ctx, `
			UPDATE booking.events
			   SET published_at = CASE WHEN $2 THEN COALESCE(published_at, now()) ELSE NULL END
			 WHERE id = $1::uuid`, eventID, published)
		if err != nil {
			return mapEventWriteErr(err, "events: set published")
		}
		return nil
	})
}

// Cancel stores status CANCELLED (idempotent: cancelling a cancelled event changes nothing and writes no audit row).
// actor is written to config_audit.changed_by.
func (r *EventWriteRepo) Cancel(ctx context.Context, eventID, actor string) error {
	return pkgpg.WithTx(ctx, r.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		if _, err := lockEvent(ctx, tx, eventID); err != nil {
			return err
		}
		if err := setActor(ctx, tx, actor); err != nil {
			return mapEventWriteErr(err, "events: cancel")
		}
		if _, err := tx.Exec(ctx, `UPDATE booking.events SET status = 'CANCELLED' WHERE id = $1::uuid`, eventID); err != nil {
			return mapEventWriteErr(err, "events: cancel")
		}
		return nil
	})
}
