package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	inbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

// Shared lock and counter statements of the unit of work. Lock order (§9.0): admission → booking → supply → quota →
// category. Each helper assumes the caller already holds the locks that come before its own.

// bookingRefs is the unlocked read that finds a booking's event and admission, so the admission can be locked
// before the booking row (§9.0). A missing booking is inbound.ErrNotFound.
func bookingRefs(ctx context.Context, tx pgx.Tx, bookingID string) (eventID, admissionID string, err error) {
	err = tx.QueryRow(ctx, `
		SELECT event_id::text, admission_id::text FROM booking.bookings WHERE id=$1::uuid`,
		bookingID).Scan(&eventID, &admissionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", inbound.ErrNotFound
	}
	return eventID, admissionID, err
}

// lockEventSupply takes the event_supply row lock that serialises every stock change of one event.
func lockEventSupply(ctx context.Context, tx pgx.Tx, eventID string) error {
	_, err := tx.Exec(ctx, `SELECT 1 FROM booking.event_supply WHERE event_id=$1::uuid FOR UPDATE`, eventID)
	return err
}

// lockCategory takes one category_inventories row lock.
func lockCategory(ctx context.Context, tx pgx.Tx, categoryID string) error {
	_, err := tx.Exec(ctx, `SELECT 1 FROM booking.category_inventories WHERE category_id=$1::uuid FOR UPDATE`, categoryID)
	return err
}

// endEditableAdmission converts an EDITABLE admission to CONVERTED(BOOKING_ENDED) once its booking reached a
// terminal state, returning any still-reserved units first. The caller holds the admission and supply locks and
// has checked that the admission is EDITABLE.
func endEditableAdmission(ctx context.Context, tx pgx.Tx, eventID, admissionID string) error {
	if err := releaseOutstanding(ctx, tx, eventID, admissionID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		UPDATE booking.admissions
		   SET status='CONVERTED', outstanding_units=0, lock_reason='BOOKING_ENDED',
		       locked_at=now(), ended_at=now(), end_reason='CONVERTED'
		 WHERE id=$1::uuid AND status='EDITABLE'`, admissionID)
	return err
}

// releaseOutstanding returns an admission's still-reserved units to free stock and zeroes its counter. The
// admission row is already locked by the caller. After the ADR-030 hold the counter is 0, so this only
// matters for rows created before migration 0009 or admissions that never reached hold.
func releaseOutstanding(ctx context.Context, tx pgx.Tx, eventID, admissionID string) error {
	var outstanding int
	if err := tx.QueryRow(ctx, `SELECT outstanding_units FROM booking.admissions WHERE id=$1::uuid`, admissionID).Scan(&outstanding); err != nil {
		return err
	}
	if outstanding == 0 {
		return nil
	}
	if _, err := tx.Exec(ctx, `UPDATE booking.event_supply SET allocated_units=allocated_units-$2, version=version+1, updated_at=now() WHERE event_id=$1::uuid`, eventID, outstanding); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE booking.admissions SET outstanding_units=0 WHERE id=$1::uuid`, admissionID)
	return err
}

// heldByCategory counts a booking's HELD items per category.
func heldByCategory(ctx context.Context, tx pgx.Tx, bookingID string) (map[string]int, error) {
	rows, err := tx.Query(ctx, `SELECT category_id::text, count(*) FROM booking.booking_items
		WHERE booking_id=$1::uuid AND item_status='HELD' GROUP BY category_id`, bookingID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var cat string
		var c int
		if err := rows.Scan(&cat, &c); err != nil {
			return nil, err
		}
		out[cat] = c
	}
	return out, rows.Err()
}

// releaseHeldItems gives back the stock a booking still holds: its HELD items become RELEASED, and the category,
// event-supply and buyer-quota held counters drop by the same amount. byCat is the booking's HELD units per category
// (heldByCategory). The caller holds the supply lock and the booking row. Does nothing when the booking has no HELD
// items, so a repeated call cannot release twice.
func releaseHeldItems(ctx context.Context, tx pgx.Tx, bookingID, eventID string, buyerHash []byte, byCat map[string]int) error {
	if sum(byCat) == 0 {
		return nil
	}
	if _, err := tx.Exec(ctx, `
		UPDATE booking.booking_items SET item_status='RELEASED'
		 WHERE booking_id=$1::uuid AND item_status='HELD'`, bookingID); err != nil {
		return err
	}
	_, err := moveHeldCounters(ctx, tx, eventID, buyerHash, byCat, false)
	return err
}

// counters are the supply and buyer-quota values after a counter move. paid and max are nil when the buyer's quota
// row does not exist.
type counters struct {
	allocated, held, sold, total int
	paid, max                    *int
}

// moveHeldCounters moves the held units of byCat out of held: to sold when sold is true (settle), or back to free
// stock when false (release). The category, buyer-quota and event-supply counters change in one statement, and the
// supply and quota values after the change come back for the sold-out and quota checks. The caller holds the supply lock.
func moveHeldCounters(ctx context.Context, tx pgx.Tx, eventID string, buyerHash []byte, byCat map[string]int, sold bool) (counters, error) {
	catIDs, catCounts := catArrays(byCat)
	n := sum(byCat)
	soldMul := 0
	if sold {
		soldMul = 1
	}
	var c counters
	if err := tx.QueryRow(ctx, `
		WITH cat AS (
			UPDATE booking.category_inventories ci
			   SET held_quantity = ci.held_quantity - u.n,
			       sold_quantity = ci.sold_quantity + $4::int * u.n,
			       updated_at = now()
			  FROM unnest($1::uuid[], $2::int[]) AS u(category_id, n)
			 WHERE ci.category_id = u.category_id),
		     q AS (
			UPDATE booking.buyer_quotas
			   SET held_quantity = held_quantity - $3::int,
			       paid_quantity = paid_quantity + $4::int * $3::int,
			       updated_at = now()
			 WHERE event_id = $5::uuid AND buyer_nik_hash = $6
			RETURNING paid_quantity, max_quantity),
		     s AS (
			UPDATE booking.event_supply
			   SET held_units = held_units - $3::int,
			       sold_units = sold_units + $4::int * $3::int,
			       version = version + 1,
			       updated_at = now()
			 WHERE event_id = $5::uuid
			RETURNING allocated_units, held_units, sold_units, total_capacity)
		SELECT s.allocated_units, s.held_units, s.sold_units, s.total_capacity, q.paid_quantity, q.max_quantity
		  FROM s LEFT JOIN q ON true`,
		catIDs, catCounts, n, soldMul, eventID, buyerHash).
		Scan(&c.allocated, &c.held, &c.sold, &c.total, &c.paid, &c.max); err != nil {
		return counters{}, err
	}
	return c, nil
}

// countByCategory counts the requested lines per category.
func countByCategory(lines []outbound.HoldLine) map[string]int {
	out := map[string]int{}
	for _, l := range lines {
		out[l.CategoryID]++
	}
	return out
}

// catArrays splits a per-category unit count into the parallel arrays the counter statements unnest. The counts stay
// Go ints: pgx encodes them into the statement's int[] parameter and refuses a value outside int4, so there is no
// unchecked narrowing here (gosec G115).
func catArrays(byCat map[string]int) ([]string, []int) {
	ids := make([]string, 0, len(byCat))
	counts := make([]int, 0, len(byCat))
	for c, k := range byCat {
		ids = append(ids, c)
		counts = append(counts, k)
	}
	return ids, counts
}

func union(a, b map[string]int) map[string]struct{} {
	out := map[string]struct{}{}
	for k := range a {
		out[k] = struct{}{}
	}
	for k := range b {
		out[k] = struct{}{}
	}
	return out
}

func sum(m map[string]int) int {
	t := 0
	for _, v := range m {
		t += v
	}
	return t
}
