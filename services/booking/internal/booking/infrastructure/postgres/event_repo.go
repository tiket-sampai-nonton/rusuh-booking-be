// Package postgres implements the Booking outbound repositories and the unit of work over pgx. The
// schema is booking (search_path set by the pool DSN). Errors are mapped to domain errors here.
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	pkgpg "github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/postgres"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/entity"
	inbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
)

// EventRepo reads the event catalog and config (read-only).
type EventRepo struct{ pool *pgxpool.Pool }

// NewEventRepo returns the repository.
func NewEventRepo(pool *pgxpool.Pool) *EventRepo { return &EventRepo{pool: pool} }

const eventCols = `e.id::text, e.slug, e.name, e.venue, e.starts_at, e.sales_starts_at, e.sales_ends_at, e.status,
	e.max_tickets_per_order, e.max_tickets_per_buyer_nik, e.allow_mixed_categories,
	e.selection_ttl_seconds, e.payment_ttl_seconds, e.claim_window_seconds,
	e.max_attendee_checks_per_session, e.selection_edit_enabled, e.max_selection_edits, e.selection_submit_grace_seconds`

func scanEvent(row pgx.Row, minPrice *int64) (entity.Event, error) {
	var e entity.Event
	dest := []any{
		&e.ID, &e.Slug, &e.Name, &e.Venue, &e.StartsAt, &e.SalesStartsAt, &e.SalesEndsAt, &e.Status,
		&e.MaxTicketsPerOrder, &e.MaxTicketsPerBuyerNik, &e.AllowMixedCategories,
		&e.SelectionTTLSeconds, &e.PaymentTTLSeconds, &e.ClaimWindowSeconds,
		&e.MaxAttendeeChecks, &e.SelectionEditEnabled, &e.MaxSelectionEdits, &e.SubmitGraceSeconds,
	}
	if minPrice != nil {
		dest = append(dest, minPrice)
	}
	err := row.Scan(dest...)
	return e, err
}

// ListEvents returns catalog rows filtered by the query.
func (r *EventRepo) ListEvents(ctx context.Context, q dto.EventListQuery) ([]entity.Event, error) {
	order := "e.starts_at ASC"
	switch q.Sort {
	case "PRICE_ASC":
		order = "min_price_minor ASC"
	case "PRICE_DESC":
		order = "min_price_minor DESC"
	}
	sql := fmt.Sprintf(`
		SELECT %s, COALESCE(MIN(c.price_minor), 0) AS min_price_minor
		  FROM booking.events e
		  LEFT JOIN booking.ticket_categories c ON c.event_id = e.id
		 WHERE ($1 = 'ALL' OR e.status = $1)
		   AND ($2 = '' OR e.venue ILIKE '%%' || $2 || '%%')
		 GROUP BY e.id
		 ORDER BY %s`, eventCols, order)

	rows, err := r.pool.Query(ctx, sql, q.Status, q.City)
	if err != nil {
		return nil, fmt.Errorf("events: list: %w", err)
	}
	defer rows.Close()
	var out []entity.Event
	for rows.Next() {
		var minPrice int64
		e, err := scanEvent(rows, &minPrice)
		if err != nil {
			return nil, fmt.Errorf("events: scan: %w", err)
		}
		e.MinPriceMinor = minPrice
		out = append(out, e)
	}
	return out, rows.Err()
}

// GetByID returns one event or inbound.ErrNotFound.
func (r *EventRepo) GetByID(ctx context.Context, eventID string) (entity.Event, error) {
	sql := fmt.Sprintf(`SELECT %s FROM booking.events e WHERE e.id = $1::uuid`, eventCols)
	e, err := scanEvent(r.pool.QueryRow(ctx, sql, eventID), nil)
	switch {
	case errors.Is(err, pgx.ErrNoRows), pkgpg.IsCode(err, "22P02"):
		return entity.Event{}, inbound.ErrNotFound
	case err != nil:
		return entity.Event{}, fmt.Errorf("events: get: %w", err)
	}
	return e, nil
}

// GetDetail returns an event with its categories and live availability.
func (r *EventRepo) GetDetail(ctx context.Context, eventID string) (entity.EventDetail, error) {
	event, err := r.GetByID(ctx, eventID)
	if err != nil {
		return entity.EventDetail{}, err
	}
	rows, err := r.pool.Query(ctx, `
		SELECT c.id::text, c.code, c.name, c.price_minor,
		       (ci.capacity - ci.held_quantity - ci.sold_quantity) AS available
		  FROM booking.ticket_categories c
		  JOIN booking.category_inventories ci ON ci.category_id = c.id
		 WHERE c.event_id = $1::uuid
		 ORDER BY c.display_order`, eventID)
	if err != nil {
		return entity.EventDetail{}, fmt.Errorf("events: detail categories: %w", err)
	}
	defer rows.Close()
	var cats []entity.CategoryDetail
	for rows.Next() {
		var c entity.Category
		var available int
		if err := rows.Scan(&c.ID, &c.Code, &c.Name, &c.PriceMinor, &available); err != nil {
			return entity.EventDetail{}, fmt.Errorf("events: detail scan: %w", err)
		}
		c.EventID = eventID
		if available < 0 {
			available = 0
		}
		cats = append(cats, entity.CategoryDetail{Category: c, Available: available})
	}
	if err := rows.Err(); err != nil {
		return entity.EventDetail{}, err
	}
	return entity.EventDetail{Event: event, Categories: cats}, nil
}
