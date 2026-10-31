package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	pkgpg "github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/postgres"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/entity"
	inbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
)

// BookingRepo reads booking read models.
type BookingRepo struct{ pool *pgxpool.Pool }

// NewBookingRepo returns the repository.
func NewBookingRepo(pool *pgxpool.Pool) *BookingRepo { return &BookingRepo{pool: pool} }

const bookingCols = `id::text, public_ref, event_id::text, admission_id::text, visitor_id::text,
	buyer_nik_hash, COALESCE(buyer_email_enc, ''), status, currency, amount_minor, item_quantity, version,
	COALESCE(account_id::text, ''), created_at, payment_expires_at, payment_started_at, verification_until, paid_at,
	COALESCE((SELECT a.buyer_nik_masked FROM booking.admissions a WHERE a.id = bookings.admission_id), '')`

func scanBooking(row pgx.Row) (entity.Booking, error) {
	var b entity.Booking
	err := row.Scan(
		&b.ID, &b.PublicRef, &b.EventID, &b.AdmissionID, &b.VisitorID,
		&b.BuyerNIKHash, &b.BuyerEmailEnc, &b.Status, &b.Currency, &b.AmountMinor, &b.ItemQuantity, &b.Version,
		&b.AccountID, &b.CreatedAt, &b.PaymentExpiresAt, &b.PaymentStartedAt, &b.VerificationUntil, &b.PaidAt, &b.BuyerNIKMasked,
	)
	return b, err
}

// GetByID returns one booking plus its active items, or inbound.ErrNotFound.
func (r *BookingRepo) GetByID(ctx context.Context, id string) (entity.Booking, error) {
	b, err := scanBooking(r.pool.QueryRow(ctx, `SELECT `+bookingCols+` FROM booking.bookings WHERE id = $1::uuid`, id))
	switch {
	case errors.Is(err, pgx.ErrNoRows), pkgpg.IsCode(err, "22P02"):
		return entity.Booking{}, inbound.ErrNotFound
	case err != nil:
		return entity.Booking{}, fmt.Errorf("bookings: get: %w", err)
	}
	items, err := r.items(ctx, id)
	if err != nil {
		return entity.Booking{}, err
	}
	b.Items = items
	return b, nil
}

func (r *BookingRepo) items(ctx context.Context, bookingID string) ([]entity.BookingItem, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id::text, booking_id::text, event_id::text, category_id::text, unit_price_minor,
		       attendee_nik_hash, attendee_nik_enc, attendee_nik_masked, attendee_name, item_status
		  FROM booking.booking_items WHERE booking_id = $1::uuid ORDER BY created_at`, bookingID)
	if err != nil {
		return nil, fmt.Errorf("bookings: items: %w", err)
	}
	defer rows.Close()
	var out []entity.BookingItem
	for rows.Next() {
		var it entity.BookingItem
		if err := rows.Scan(&it.ID, &it.BookingID, &it.EventID, &it.CategoryID, &it.UnitPriceMinor,
			&it.AttendeeNIKHash, &it.AttendeeNIKEnc, &it.AttendeeNIKMasked, &it.AttendeeName, &it.Status); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// ListByAccount returns the buyer's bookings with keyset pagination on created_at (newest first).
func (r *BookingRepo) ListByAccount(ctx context.Context, accountID, cursor string, limit int) ([]dto.MyBookingItem, string, error) {
	before := time.Now().UTC().Add(24 * time.Hour)
	if cursor != "" {
		if t, err := time.Parse(time.RFC3339Nano, cursor); err == nil {
			before = t
		}
	}
	rows, err := r.pool.Query(ctx, `
		SELECT b.id::text, b.public_ref, e.name, b.status, b.amount_minor, b.created_at
		  FROM booking.bookings b JOIN booking.events e ON e.id = b.event_id
		 WHERE b.account_id = $1::uuid AND b.created_at < $2
		 ORDER BY b.created_at DESC LIMIT $3`, accountID, before, limit+1)
	if err != nil {
		return nil, "", fmt.Errorf("bookings: list by account: %w", err)
	}
	defer rows.Close()
	var items []dto.MyBookingItem
	for rows.Next() {
		var m dto.MyBookingItem
		if err := rows.Scan(&m.BookingID, &m.PublicRef, &m.EventName, &m.Status, &m.AmountMinor, &m.CreatedAt); err != nil {
			return nil, "", err
		}
		items = append(items, m)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(items) > limit {
		next = items[limit-1].CreatedAt.Format(time.RFC3339Nano)
		items = items[:limit]
	}
	return items, next, nil
}

// DueBookingIDs returns ids of bookings whose payment window lapsed.
func (r *BookingRepo) DueBookingIDs(ctx context.Context, now time.Time, limit int) ([]string, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id::text FROM booking.bookings
		 WHERE status IN ('PENDING_PAYMENT','VERIFYING') AND payment_expires_at < $1
		 ORDER BY payment_expires_at LIMIT $2`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("bookings: due: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
