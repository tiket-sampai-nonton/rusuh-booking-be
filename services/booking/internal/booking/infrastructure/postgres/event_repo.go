// Package postgres implements the Booking outbound repositories and the unit of work over pgx. The
// schema is booking (search_path set by the pool DSN). Errors are mapped to domain errors here.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	pkgpg "github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/postgres"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/entity"
	inbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

// EventRepo reads the event catalog and config (read-only).
type EventRepo struct{ pool *pgxpool.Pool }

// NewEventRepo returns the repository.
func NewEventRepo(pool *pgxpool.Pool) *EventRepo { return &EventRepo{pool: pool} }

const eventCols = `e.id::text, e.slug, e.name, e.venue, e.city, e.genre,
	COALESCE(e.image_url, ''), COALESCE(e.banner_url, ''), COALESCE(e.thumbnail_url, ''),
	e.starts_at, e.sales_starts_at, e.sales_ends_at, e.status,
	e.max_tickets_per_order, e.max_tickets_per_buyer_nik, e.allow_mixed_categories,
	e.selection_ttl_seconds, e.payment_ttl_seconds, e.claim_window_seconds,
	e.max_attendee_checks_per_session, e.selection_edit_enabled, e.max_selection_edits, e.selection_submit_grace_seconds,
	(e.published_at IS NOT NULL),
	` + stockSoldOutSQL

// stockSoldOutSQL derives SOLD_OUT from supply (AC-C4.2, D-C4.2): every unit is held or sold. It is the same rule as the
// detail's categories (sum of capacity - held - sold = total - held - sold), so list, filter and detail agree.
// It has no $n placeholder, so filterWhereAt can renumber eventFilterWhere around it.
const stockSoldOutSQL = `EXISTS (SELECT 1 FROM booking.event_supply s
		WHERE s.event_id = e.id AND s.total_capacity > 0 AND s.held_units + s.sold_units >= s.total_capacity)`

// scanEvent scans eventCols, then minPrice when given, then any extra destinations (columns appended to the query).
func scanEvent(row pgx.Row, minPrice *int64, extra ...any) (entity.Event, error) {
	var e entity.Event
	dest := []any{
		&e.ID, &e.Slug, &e.Name, &e.Venue, &e.City, &e.Genres,
		&e.ImageURL, &e.BannerURL, &e.ThumbnailURL,
		&e.StartsAt, &e.SalesStartsAt, &e.SalesEndsAt, &e.Status,
		&e.MaxTicketsPerOrder, &e.MaxTicketsPerBuyerNik, &e.AllowMixedCategories,
		&e.SelectionTTLSeconds, &e.PaymentTTLSeconds, &e.ClaimWindowSeconds,
		&e.MaxAttendeeChecks, &e.SelectionEditEnabled, &e.MaxSelectionEdits, &e.SubmitGraceSeconds,
		&e.Published, &e.StockSoldOut,
	}
	if minPrice != nil {
		dest = append(dest, minPrice)
	}
	dest = append(dest, extra...)
	err := row.Scan(dest...)
	return e, err
}

// eventFilterWhere is the catalog filter predicate shared by ListEvents and ListFacets so both endpoints
// agree on exactly what "visible, matching the query" means (single source of truth, not duplicated).
// Positional params, in order: $1 status, $2 city, $3 timeframe, $4 genres::text[], $5 startUTC, $6 endUTC.
// The published + non-cancelled floor is always applied. Callers that want a drill-down facet zero out the
// dimension being listed (city="" or genres=nil) via filterArgs before binding.
const eventFilterWhere = `
		 WHERE ($1 = 'ALL' OR (CASE WHEN e.status IN ('CLOSED','CANCELLED') THEN e.status
		                            WHEN now() < e.sales_starts_at THEN 'NOT_STARTED'
		                            WHEN now() > e.sales_ends_at THEN 'CLOSED'
		                            WHEN ` + stockSoldOutSQL + ` THEN 'SOLD_OUT'
		                            ELSE 'AVAILABLE' END) = $1)
		   AND ($2 = '' OR lower(e.city) = lower($2))
		   AND ($3 = 'ALL'
		        OR ($3 = 'UPCOMING'   AND (e.starts_at AT TIME ZONE 'Asia/Jakarta')::date >= (now() AT TIME ZONE 'Asia/Jakarta')::date)
		        OR ($3 = 'THIS_WEEK'  AND date_trunc('week',  e.starts_at AT TIME ZONE 'Asia/Jakarta') = date_trunc('week',  now() AT TIME ZONE 'Asia/Jakarta'))
		        OR ($3 = 'THIS_MONTH' AND date_trunc('month', e.starts_at AT TIME ZONE 'Asia/Jakarta') = date_trunc('month', now() AT TIME ZONE 'Asia/Jakarta')))
		   AND (cardinality($4::text[]) = 0 OR e.genre && $4::text[])
		   AND ($5::timestamptz IS NULL OR e.starts_at >= $5::timestamptz)
		   AND ($6::timestamptz IS NULL OR e.starts_at <  $6::timestamptz)
		   AND e.published_at IS NOT NULL
		   AND (e.status <> 'CANCELLED' OR $1 = 'CANCELLED')`

// filterArgs returns the six positional args for eventFilterWhere, in order, from the query.
func filterArgs(q dto.EventListQuery) []any {
	return []any{orAll(q.Status), q.City, orAll(q.Timeframe), genresParam(q.Genres), q.StartUTC, q.EndUTC}
}

// filterWhereAt returns eventFilterWhere with its $1..$6 placeholders shifted to start at base+1, so the
// same single-source predicate can be reused several times in one statement, each bound to its own arg
// block. base=0 yields the original $1..$6; base=6 yields $7..$12; base=12 yields $13..$18. Keeping one
// predicate constant and renumbering it (rather than copying the SQL) preserves the single source of truth.
func filterWhereAt(base int) string {
	if base == 0 {
		return eventFilterWhere
	}
	// Match a whole $N token (one or more digits) and shift it, so renumbering never corrupts a multi-digit
	// placeholder (a naive per-number string replace would turn $1 inside $13 into garbage).
	return placeholderRe.ReplaceAllStringFunc(eventFilterWhere, func(tok string) string {
		n, _ := strconv.Atoi(tok[1:])
		return fmt.Sprintf("$%d", base+n)
	})
}

// placeholderRe matches a positional parameter token like $1 or $12.
var placeholderRe = regexp.MustCompile(`\$\d+`)

// catalogListLimit caps the public list (D-C7.7). The query has no keyset cursor, so a larger page would scale with
// the whole catalog on every request.
const catalogListLimit = 500

// ListEvents returns catalog rows filtered by the query, at most catalogListLimit rows in the query's sort order.
func (r *EventRepo) ListEvents(ctx context.Context, q dto.EventListQuery) ([]entity.Event, error) {
	order := "e.starts_at ASC, e.id"
	switch q.Sort {
	case "DATE_DESC":
		order = "e.starts_at DESC, e.id"
	case "PRICE_ASC":
		order = "min_price_minor ASC, e.id"
	case "PRICE_DESC":
		order = "min_price_minor DESC, e.id"
	}
	// status follows the sales window and stock like the status the API shows (stored CLOSED/CANCELLED win, a stored
	// SOLD_OUT is ignored, AC-C4.2 R13); timeframe applies to startsAt in WIB: THIS_WEEK Monday-Sunday, THIS_MONTH
	// the calendar month, UPCOMING today onwards (SRS-F-28-03).
	sql := fmt.Sprintf(`
		SELECT %s, COALESCE(MIN(c.price_minor), 0) AS min_price_minor
		  FROM booking.events e
		  LEFT JOIN booking.ticket_categories c ON c.event_id = e.id%s
		 GROUP BY e.id
		 ORDER BY %s
		 LIMIT %d`, eventCols, eventFilterWhere, order, catalogListLimit)

	rows, err := r.pool.Query(ctx, sql, filterArgs(q)...)
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

// Facets returns, in a single round-trip, the drill-down city and genre values plus the count of events
// matching the full query. All three reuse the one shared filter predicate (eventFilterWhere, renumbered
// per block) so facet values always agree with what ListEvents can show. Drill-down is intrinsic: the
// cities block applies every filter except city ($2 blanked), the genres block every filter except genres
// ($10 → empty array). The count applies the full, unmodified filter — the caller uses it for the option-c
// empty guard (count 0 ⇒ facets must read empty). Results are distinct and sorted.
//
// Param layout: $1..$6 full (count), $7..$12 city-dropped (cities), $13..$18 genres-dropped (genres).
func (r *EventRepo) Facets(ctx context.Context, q dto.EventListQuery) (entity.EventFacets, int, error) {
	cityQ := q
	cityQ.City = "" // drill-down: cities ignore the city filter
	genreQ := q
	genreQ.Genres = nil // drill-down: genres ignore the genres filter

	sql := `
		WITH cnt AS (
			SELECT count(*) AS n FROM booking.events e` + filterWhereAt(0) + `
		), cities AS (
			SELECT DISTINCT e.city AS v FROM booking.events e` + filterWhereAt(6) + `
			   AND e.city <> ''
		), genres AS (
			SELECT DISTINCT g AS v FROM booking.events e, unnest(e.genre) AS g` + filterWhereAt(12) + `
		)
		SELECT
			(SELECT n FROM cnt) AS total,
			COALESCE((SELECT array_agg(v ORDER BY v) FROM cities), '{}') AS cities,
			COALESCE((SELECT array_agg(v ORDER BY v) FROM genres), '{}') AS genres`

	args := make([]any, 0, 18)
	args = append(args, filterArgs(q)...)      // $1..$6  full
	args = append(args, filterArgs(cityQ)...)  // $7..$12 city-dropped
	args = append(args, filterArgs(genreQ)...) // $13..$18 genres-dropped

	var out entity.EventFacets
	var n int
	if err := r.pool.QueryRow(ctx, sql, args...).Scan(&n, &out.Cities, &out.Genres); err != nil {
		return entity.EventFacets{}, 0, fmt.Errorf("events: facets: %w", err)
	}
	return out, n, nil
}

// GetByID returns one event or inbound.ErrNotFound.
func (r *EventRepo) GetByID(ctx context.Context, eventID string) (entity.Event, error) {
	// description is only needed on the detail path; keeping it out of eventCols keeps the catalog list light.
	sql := fmt.Sprintf(`SELECT %s, e.description FROM booking.events e WHERE e.id = $1::uuid`, eventCols)
	var description []byte
	e, err := scanEvent(r.pool.QueryRow(ctx, sql, eventID), nil, &description)
	switch {
	case errors.Is(err, pgx.ErrNoRows), pkgpg.IsCode(err, "22P02"):
		return entity.Event{}, inbound.ErrNotFound
	case err != nil:
		return entity.Event{}, fmt.Errorf("events: get: %w", err)
	}
	e.Description = description
	return e, nil
}

// GetSupply returns the event-level ledger row.
func (r *EventRepo) GetSupply(ctx context.Context, eventID string) (entity.Supply, error) {
	var s entity.Supply
	err := r.pool.QueryRow(ctx, `SELECT event_id::text, total_capacity, allocated_units, held_units, sold_units
		FROM booking.event_supply WHERE event_id = $1::uuid`, eventID).
		Scan(&s.EventID, &s.TotalCapacity, &s.AllocatedUnits, &s.HeldUnits, &s.SoldUnits)
	switch {
	case errors.Is(err, pgx.ErrNoRows), pkgpg.IsCode(err, "22P02"):
		return entity.Supply{}, inbound.ErrNotFound
	case err != nil:
		return entity.Supply{}, fmt.Errorf("events: supply: %w", err)
	}
	return s, nil
}

// GetDetail returns an event with its categories and live availability.
func (r *EventRepo) GetDetail(ctx context.Context, eventID string) (entity.EventDetail, error) {
	event, err := r.GetByID(ctx, eventID)
	if err != nil {
		return entity.EventDetail{}, err
	}
	rows, err := r.pool.Query(ctx, `
		SELECT c.id::text, c.code, c.name, c.price_minor, ci.capacity, ci.sold_quantity,
		       GREATEST(ci.capacity - ci.held_quantity - ci.sold_quantity, 0) AS category_free,
		       GREATEST(s.total_capacity - s.allocated_units - s.held_units - s.sold_units, 0) AS event_free
		  FROM booking.ticket_categories c
		  JOIN booking.category_inventories ci ON ci.category_id = c.id
		  JOIN booking.event_supply s ON s.event_id = c.event_id
		 WHERE c.event_id = $1::uuid
		 ORDER BY c.display_order`, eventID)
	if err != nil {
		return entity.EventDetail{}, fmt.Errorf("events: detail categories: %w", err)
	}
	defer rows.Close()
	var cats []entity.CategoryDetail
	for rows.Next() {
		var c entity.Category
		var capacity, sold, categoryFree, eventFree int
		if err := rows.Scan(&c.ID, &c.Code, &c.Name, &c.PriceMinor, &capacity, &sold, &categoryFree, &eventFree); err != nil {
			return entity.EventDetail{}, fmt.Errorf("events: detail scan: %w", err)
		}
		c.EventID = eventID
		cats = append(cats, entity.CategoryDetail{
			Category: c, Capacity: capacity, Sold: sold,
			Available:    entity.BuyerAvailable(categoryFree, eventFree, 0),
			CategoryFree: categoryFree, EventFree: eventFree,
		})
	}
	if err := rows.Err(); err != nil {
		return entity.EventDetail{}, err
	}
	// Same definition as the list (COALESCE(MIN(price),0), sold-out categories included), taken from the one
	// categories snapshot read above so the listed categories and the minimum cannot disagree.
	for i, c := range cats {
		if i == 0 || c.Category.PriceMinor < event.MinPriceMinor {
			event.MinPriceMinor = c.Category.PriceMinor
		}
	}
	return entity.EventDetail{Event: event, Categories: cats}, nil
}

// orAll treats an empty filter as "no filter" (the usecase normalises, other callers may not).
func orAll(v string) string {
	if v == "" {
		return "ALL"
	}
	return v
}

// genresParam ensures a non-nil slice so pgx binds a real text[] (cardinality 0 => no filter).
func genresParam(g []string) []string {
	if g == nil {
		return []string{}
	}
	return g
}

// OnSaleAvailability returns the availability snapshot of every published event whose sales window contains now and
// that is neither closed nor cancelled, keyed by event ID. Category free is capacity - held - sold (clamped at 0);
// event free is total - allocated - held - sold (clamped at 0) from event_supply.
func (r *EventRepo) OnSaleAvailability(ctx context.Context, now time.Time) (map[string]outbound.AvailabilitySnapshot, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT e.id::text,
		       GREATEST(s.total_capacity - s.allocated_units - s.held_units - s.sold_units, 0),
		       c.id::text,
		       GREATEST(ci.capacity - ci.held_quantity - ci.sold_quantity, 0)
		  FROM booking.events e
		  JOIN booking.event_supply s ON s.event_id = e.id
		  JOIN booking.ticket_categories c ON c.event_id = e.id
		  JOIN booking.category_inventories ci ON ci.category_id = c.id
		 WHERE e.status NOT IN ('CLOSED','CANCELLED') AND e.published_at IS NOT NULL
		   AND e.sales_starts_at <= $1 AND e.sales_ends_at > $1
		 ORDER BY e.id, c.display_order`, now)
	if err != nil {
		return nil, fmt.Errorf("events: on-sale availability: %w", err)
	}
	defer rows.Close()
	out := map[string]outbound.AvailabilitySnapshot{}
	for rows.Next() {
		var eventID string
		var eventFree int
		var s outbound.CategoryAvailabilitySnap
		if err := rows.Scan(&eventID, &eventFree, &s.CategoryID, &s.Available); err != nil {
			return nil, fmt.Errorf("events: scan on-sale availability: %w", err)
		}
		snap := out[eventID]
		snap.EventFree = eventFree
		snap.Categories = append(snap.Categories, s)
		out[eventID] = snap
	}
	return out, rows.Err()
}
