package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	pkgpg "github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/postgres"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

// TicketRepo reads issued tickets.
type TicketRepo struct{ pool *pgxpool.Pool }

// NewTicketRepo returns the repository.
func NewTicketRepo(pool *pgxpool.Pool) *TicketRepo { return &TicketRepo{pool: pool} }

// ListByBookingID returns issued tickets joined with their item display fields.
func (r *TicketRepo) ListByBookingID(ctx context.Context, bookingID string) ([]entity.Ticket, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT t.id::text, t.booking_id::text, t.booking_item_id::text, t.public_code, t.issued_at,
		       c.code, bi.attendee_name, bi.attendee_nik_masked
		  FROM booking.tickets t
		  JOIN booking.booking_items bi ON bi.id = t.booking_item_id
		  JOIN booking.ticket_categories c ON c.id = bi.category_id
		 WHERE t.booking_id = $1::uuid
		 ORDER BY t.issued_at`, bookingID)
	if err != nil {
		return nil, fmt.Errorf("tickets: list: %w", err)
	}
	defer rows.Close()
	var out []entity.Ticket
	for rows.Next() {
		var t entity.Ticket
		if err := rows.Scan(&t.ID, &t.BookingID, &t.BookingItemID, &t.PublicCode, &t.IssuedAt,
			&t.CategoryCode, &t.AttendeeName, &t.AttendeeNIKMasked); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// QuotaRepo reads buyer quota.
type QuotaRepo struct{ pool *pgxpool.Pool }

// NewQuotaRepo returns the repository.
func NewQuotaRepo(pool *pgxpool.Pool) *QuotaRepo { return &QuotaRepo{pool: pool} }

// CountHeldAndPaid returns the held, paid, and max quota for a buyer on an event (0,0,3 when absent).
func (r *QuotaRepo) CountHeldAndPaid(ctx context.Context, eventID string, buyerNIKHash []byte) (held, paid, max int, err error) {
	row := r.pool.QueryRow(ctx, `
		SELECT held_quantity, paid_quantity, max_quantity
		  FROM booking.buyer_quotas WHERE event_id = $1::uuid AND buyer_nik_hash = $2`, eventID, buyerNIKHash)
	err = row.Scan(&held, &paid, &max)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, 0, 3, nil
	}
	if err != nil {
		return 0, 0, 0, fmt.Errorf("quota: count: %w", err)
	}
	return held, paid, max, nil
}

// AttendeeCheckRepo probes and records attendee NIK checks.
type AttendeeCheckRepo struct{ pool *pgxpool.Pool }

// NewAttendeeCheckRepo returns the repository.
func NewAttendeeCheckRepo(pool *pgxpool.Pool) *AttendeeCheckRepo {
	return &AttendeeCheckRepo{pool: pool}
}

// IsRegistered reports whether an attendee NIK is HELD/PAID for an event.
func (r *AttendeeCheckRepo) IsRegistered(ctx context.Context, eventID string, nikHash []byte) (bool, error) {
	var one int
	err := r.pool.QueryRow(ctx, `
		SELECT 1 FROM booking.booking_items
		 WHERE event_id = $1::uuid AND attendee_nik_hash = $2 AND item_status IN ('HELD','PAID') LIMIT 1`,
		eventID, nikHash).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("attendee: registered: %w", err)
	}
	return true, nil
}

// errAttendeeLimit aborts the RecordCheck transaction when the session cap is already full, which also rolls back
// the dedupe row inserted for the NIK.
var errAttendeeLimit = errors.New("attendee: session check limit reached")

// RecordCheck dedupes a NIK within the admission and, when it is new, consumes one of maxChecks checks in the same
// transaction. The conditional UPDATE serialises concurrent checks on the admission row, so the counter never
// exceeds maxChecks.
func (r *AttendeeCheckRepo) RecordCheck(ctx context.Context, admissionID string, nikHash []byte, maxChecks int) (outbound.AttendeeCheckRecord, error) {
	var rec outbound.AttendeeCheckRecord
	err := pkgpg.WithTx(ctx, r.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			INSERT INTO booking.attendee_checks (admission_id, attendee_nik_hash)
			VALUES ($1::uuid, $2) ON CONFLICT (admission_id, attendee_nik_hash) DO NOTHING`, admissionID, nikHash)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			// Already checked this session; not counted again.
			return tx.QueryRow(ctx, `SELECT attendee_checks FROM booking.admissions WHERE id = $1::uuid`, admissionID).Scan(&rec.Used)
		}
		err = tx.QueryRow(ctx, `
			UPDATE booking.admissions SET attendee_checks = attendee_checks + 1
			 WHERE id = $1::uuid AND attendee_checks < $2
			RETURNING attendee_checks`, admissionID, maxChecks).Scan(&rec.Used)
		if errors.Is(err, pgx.ErrNoRows) {
			return errAttendeeLimit
		}
		if err != nil {
			return err
		}
		rec.Counted = true
		return nil
	})
	if errors.Is(err, errAttendeeLimit) {
		return outbound.AttendeeCheckRecord{LimitReached: true, Used: maxChecks}, nil
	}
	if err != nil {
		return outbound.AttendeeCheckRecord{}, fmt.Errorf("attendee: record: %w", err)
	}
	return rec, nil
}

// IdempotencyStore deduplicates mutating requests.
type IdempotencyStore struct{ pool *pgxpool.Pool }

// NewIdempotencyStore returns the store.
func NewIdempotencyStore(pool *pgxpool.Pool) *IdempotencyStore { return &IdempotencyStore{pool: pool} }

// Begin inserts an IN_PROGRESS record, or returns the existing one (found=true). An IN_PROGRESS record older than
// 60 seconds, or any record past expires_at, is taken over: the upsert then affects one row and the caller proceeds
// as a fresh request (found=false).
func (s *IdempotencyStore) Begin(ctx context.Context, scope, ownerID, key string, requestHash []byte, ttl time.Duration) (outbound.IdempotencyRecord, bool, error) {
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO booking.idempotency_records (scope, owner_id, idempotency_key, request_hash, state, expires_at)
		VALUES ($1, $2::uuid, $3, $4, 'IN_PROGRESS', now() + $5::interval)
		ON CONFLICT (scope, owner_id, idempotency_key) DO UPDATE
		   SET request_hash=EXCLUDED.request_hash, state='IN_PROGRESS', response_status=NULL, response_body=NULL,
		       created_at=now(), expires_at=EXCLUDED.expires_at
		 WHERE booking.idempotency_records.expires_at <= now()
		    OR (booking.idempotency_records.state='IN_PROGRESS' AND booking.idempotency_records.created_at < now() - interval '60 seconds')`,
		scope, ownerID, key, requestHash, fmt.Sprintf("%d seconds", int(ttl.Seconds())))
	if err != nil {
		return outbound.IdempotencyRecord{}, false, fmt.Errorf("idem: begin: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return outbound.IdempotencyRecord{State: "IN_PROGRESS", RequestHash: requestHash}, false, nil
	}
	var rec outbound.IdempotencyRecord
	var status *int
	var body []byte
	err = s.pool.QueryRow(ctx, `
		SELECT state, request_hash, response_status, response_body
		  FROM booking.idempotency_records WHERE scope=$1 AND owner_id=$2::uuid AND idempotency_key=$3`,
		scope, ownerID, key).Scan(&rec.State, &rec.RequestHash, &status, &body)
	if err != nil {
		return outbound.IdempotencyRecord{}, false, fmt.Errorf("idem: load: %w", err)
	}
	if status != nil {
		rec.ResponseStatus = *status
	}
	rec.ResponseBody = body
	return rec, true, nil
}

// Complete records the final response for a key.
func (s *IdempotencyStore) Complete(ctx context.Context, scope, ownerID, key string, status int, body []byte) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE booking.idempotency_records
		   SET state='COMPLETED', response_status=$4, response_body=$5
		 WHERE scope=$1 AND owner_id=$2::uuid AND idempotency_key=$3`, scope, ownerID, key, status, body)
	if err != nil {
		return fmt.Errorf("idem: complete: %w", err)
	}
	return nil
}

// Abort removes an IN_PROGRESS record (same request hash) after a typed business refusal, so the client
// can retry with the same key. A COMPLETED record is never touched.
func (s *IdempotencyStore) Abort(ctx context.Context, scope, ownerID, key string, requestHash []byte) error {
	_, err := s.pool.Exec(ctx, `
		DELETE FROM booking.idempotency_records
		 WHERE scope=$1 AND owner_id=$2::uuid AND idempotency_key=$3 AND state='IN_PROGRESS' AND request_hash=$4`,
		scope, ownerID, key, requestHash)
	if err != nil {
		return fmt.Errorf("idem: abort: %w", err)
	}
	return nil
}
