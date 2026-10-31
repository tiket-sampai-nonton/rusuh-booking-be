//go:build e2e

package helpers

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// SeedEvent creates one AVAILABLE event with a single VIP category and returns eventID, categoryID.
// The booking.events schema uses "booking." prefix (set_config search_path = booking).
func SeedEvent(ctx context.Context, pool *pgxpool.Pool, capacity int) (eventID, categoryID string, err error) {
	slug := fmt.Sprintf("test-event-%d", time.Now().UnixNano())
	err = pool.QueryRow(ctx, `
		INSERT INTO booking.events (
			slug, name, venue, city, starts_at, sales_starts_at, sales_ends_at,
			status, max_tickets_per_order, max_tickets_per_buyer_nik,
			selection_ttl_seconds, payment_ttl_seconds, selection_submit_grace_seconds,
			claim_window_seconds, selection_edit_enabled, max_selection_edits,
			allow_mixed_categories)
		VALUES ($1,'Test Event','GBK','Jakarta',
			now()+interval '30 days', now()-interval '1 hour', now()+interval '7 days',
			'AVAILABLE', 3, 3, 300, 600, 5, 180, true, 3, true)
		RETURNING id::text`, slug).Scan(&eventID)
	if err != nil {
		return
	}
	err = pool.QueryRow(ctx, `
		INSERT INTO booking.ticket_categories (event_id, code, name, price_minor)
		VALUES ($1::uuid, 'VIP', 'VIP', 150000) RETURNING id::text`, eventID).Scan(&categoryID)
	if err != nil {
		return
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO booking.category_inventories (category_id, event_id, capacity)
		VALUES ($1::uuid, $2::uuid, $3)`, categoryID, eventID, capacity)
	if err != nil {
		return
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO booking.event_supply (event_id, total_capacity)
		VALUES ($1::uuid, $2)`, eventID, capacity)
	return
}

// CleanupEvent deletes all booking data seeded for an event (test isolation).
func CleanupEvent(ctx context.Context, pool *pgxpool.Pool, eventID string) error {
	queries := []string{
		`DELETE FROM booking.outbox_events WHERE aggregate_id = $1::uuid`,
		`DELETE FROM booking.tickets WHERE booking_id IN (SELECT id FROM booking.bookings WHERE event_id=$1::uuid)`,
		`DELETE FROM booking.booking_events WHERE stream_id IN (SELECT id FROM booking.bookings WHERE event_id=$1::uuid)`,
		`DELETE FROM booking.booking_items WHERE booking_id IN (SELECT id FROM booking.bookings WHERE event_id=$1::uuid)`,
		`DELETE FROM booking.bookings WHERE event_id=$1::uuid`,
		`DELETE FROM booking.buyer_quotas WHERE event_id=$1::uuid`,
		`DELETE FROM booking.category_inventories WHERE event_id=$1::uuid`,
		`DELETE FROM booking.event_supply WHERE event_id=$1::uuid`,
		`DELETE FROM booking.ticket_categories WHERE event_id=$1::uuid`,
		`DELETE FROM booking.admissions WHERE event_id=$1::uuid`,
		`DELETE FROM booking.events WHERE id=$1::uuid`,
	}
	for _, q := range queries {
		if _, err := pool.Exec(ctx, q, eventID); err != nil {
			return fmt.Errorf("cleanup %q: %w", q[:40], err)
		}
	}
	return nil
}

// BookingStatus returns the current status of a booking row.
func BookingStatus(ctx context.Context, pool *pgxpool.Pool, bookingID string) (string, error) {
	var s string
	err := pool.QueryRow(ctx, `SELECT status FROM booking.bookings WHERE id=$1::uuid`, bookingID).Scan(&s)
	return s, err
}

// OutboxCount returns the number of outbox rows with the given routing key for a booking.
func OutboxCount(ctx context.Context, pool *pgxpool.Pool, routingKey, aggregateID string) (int, error) {
	var n int
	err := pool.QueryRow(ctx,
		`SELECT count(*) FROM booking.outbox_events WHERE routing_key=$1 AND aggregate_id=$2::uuid`,
		routingKey, aggregateID).Scan(&n)
	return n, err
}

// AllocatedUnits returns allocated_units for an event.
func AllocatedUnits(ctx context.Context, pool *pgxpool.Pool, eventID string) (int, error) {
	var n int
	err := pool.QueryRow(ctx,
		`SELECT allocated_units FROM booking.event_supply WHERE event_id=$1::uuid`, eventID).Scan(&n)
	return n, err
}

// HeldUnits returns held_units for an event.
func HeldUnits(ctx context.Context, pool *pgxpool.Pool, eventID string) (int, error) {
	var n int
	err := pool.QueryRow(ctx,
		`SELECT held_units FROM booking.event_supply WHERE event_id=$1::uuid`, eventID).Scan(&n)
	return n, err
}

// TicketCount returns number of tickets issued for a booking.
func TicketCount(ctx context.Context, pool *pgxpool.Pool, bookingID string) (int, error) {
	var n int
	err := pool.QueryRow(ctx,
		`SELECT count(*) FROM booking.tickets WHERE booking_id=$1::uuid`, bookingID).Scan(&n)
	return n, err
}

// ForceExpireBooking sets payment_expires_at in the past so the sweeper picks it up.
func ForceExpireBooking(ctx context.Context, pool *pgxpool.Pool, bookingID string) error {
	_, err := pool.Exec(ctx,
		`UPDATE booking.bookings SET payment_expires_at = now() - interval '1 minute' WHERE id=$1::uuid`,
		bookingID)
	return err
}

// WaitForBookingStatus polls until the booking reaches the target status or times out.
func WaitForBookingStatus(ctx context.Context, pool *pgxpool.Pool, bookingID, target string, timeoutSec int) error {
	deadline := time.Now().Add(time.Duration(timeoutSec) * time.Second)
	for time.Now().Before(deadline) {
		status, err := BookingStatus(ctx, pool, bookingID)
		if err == nil && status == target {
			return nil
		}
		time.Sleep(time.Second)
	}
	status, _ := BookingStatus(ctx, pool, bookingID)
	return fmt.Errorf("booking %s: expected status %q within %ds, got %q", bookingID, target, timeoutSec, status)
}
