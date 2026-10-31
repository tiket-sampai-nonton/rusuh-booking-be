package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	pkgpg "github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/postgres"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
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
				max_tickets_per_order, max_tickets_per_buyer_nik, allow_mixed_categories)
			VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, 'DRAFT', $8, $9, $10)`,
			id, req.Slug, req.Name, req.Venue, req.StartsAt, req.SalesStartsAt, req.SalesEndsAt,
			req.MaxTicketsPerOrder, req.MaxTicketsPerBuyerNIK, req.AllowMixedCategories); err != nil {
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
func (r *EventWriteRepo) UpdateConfig(ctx context.Context, eventID string, req dto.UpdateEventConfigRequest) error {
	sets := make([]string, 0, 4)
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
	if len(sets) == 0 {
		// No-op: nothing to change. Still confirm the event exists.
		var exists bool
		err := r.pool.QueryRow(ctx, `SELECT true FROM booking.events WHERE id = $1::uuid`, eventID).Scan(&exists)
		switch {
		case errors.Is(err, pgx.ErrNoRows), pkgpg.IsCode(err, "22P02"):
			return inbound.ErrNotFound
		case err != nil:
			return mapEventWriteErr(err, "events: update config")
		}
		return nil
	}

	sql := fmt.Sprintf(`UPDATE booking.events SET %s WHERE id = $1::uuid`, strings.Join(sets, ", "))
	tag, err := r.pool.Exec(ctx, sql, args...)
	if err != nil {
		return mapEventWriteErr(err, "events: update config")
	}
	if tag.RowsAffected() == 0 {
		return inbound.ErrNotFound
	}
	return nil
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
