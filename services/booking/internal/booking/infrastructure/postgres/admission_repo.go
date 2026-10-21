package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	pkgpg "github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/postgres"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/entity"
	vo "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/valueobject"
	inbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

// AdmissionRepo manages admissions.
type AdmissionRepo struct{ pool *pgxpool.Pool }

// NewAdmissionRepo returns the repository.
func NewAdmissionRepo(pool *pgxpool.Pool) *AdmissionRepo { return &AdmissionRepo{pool: pool} }

const admissionCols = `id::text, event_id::text, queue_entry_id::text, queue_seq, visitor_id::text,
	buyer_nik_hash, buyer_email, COALESCE(buyer_nik_masked, ''), units, status,
	COALESCE(admission_jti::text, ''), attendee_checks, edit_count, COALESCE(account_id::text, ''),
	cfg_selection_ttl_seconds, cfg_payment_ttl_seconds, cfg_submit_grace_seconds,
	cfg_edit_enabled, cfg_max_edits, cfg_allow_mixed_categories,
	reserved_at, expires_at`

func scanAdmission(row pgx.Row) (entity.Admission, error) {
	var a entity.Admission
	err := row.Scan(
		&a.ID, &a.EventID, &a.QueueEntryID, &a.QueueSeq, &a.VisitorID,
		&a.BuyerNIKHash, &a.BuyerEmail, &a.BuyerNIKMasked, &a.Units, &a.Status,
		&a.AdmissionJTI, &a.AttendeeChecks, &a.EditCount, &a.AccountID,
		&a.CfgSelectionTTLSeconds, &a.CfgPaymentTTLSeconds, &a.CfgSubmitGraceSeconds,
		&a.CfgEditEnabled, &a.CfgMaxEdits, &a.CfgAllowMixedCategories,
		&a.ReservedAt, &a.ExpiresAt,
	)
	return a, err
}

// GetByID returns one admission or inbound.ErrNotFound.
func (r *AdmissionRepo) GetByID(ctx context.Context, id string) (entity.Admission, error) {
	a, err := scanAdmission(r.pool.QueryRow(ctx, `SELECT `+admissionCols+` FROM booking.admissions WHERE id = $1::uuid`, id))
	return mapAdmissionErr(a, err)
}

// FindByJTI returns the admission bound to a one-time jti.
func (r *AdmissionRepo) FindByJTI(ctx context.Context, jti string) (entity.Admission, error) {
	a, err := scanAdmission(r.pool.QueryRow(ctx, `SELECT `+admissionCols+` FROM booking.admissions WHERE admission_jti = $1::uuid`, jti))
	return mapAdmissionErr(a, err)
}

// FindActiveByVisitor returns the active admission for a visitor on an event.
func (r *AdmissionRepo) FindActiveByVisitor(ctx context.Context, eventID, visitorID string) (entity.Admission, error) {
	a, err := scanAdmission(r.pool.QueryRow(ctx, `SELECT `+admissionCols+`
		FROM booking.admissions
		WHERE event_id = $1::uuid AND visitor_id = $2::uuid
		  AND status IN ('RESERVED','ACTIVE','EDITABLE')`, eventID, visitorID))
	return mapAdmissionErr(a, err)
}

// FindActiveByEvent is an alias used by current-session recovery.
func (r *AdmissionRepo) FindActiveByEvent(ctx context.Context, eventID, visitorID string) (entity.Admission, error) {
	return r.FindActiveByVisitor(ctx, eventID, visitorID)
}

// Activate flips a RESERVED admission to ACTIVE by its one-time jti.
func (r *AdmissionRepo) Activate(ctx context.Context, jti string, at time.Time) (entity.Admission, error) {
	// jti in the admission token is the admission's primary key (id), which Queue knows as allocationId.
	a, err := scanAdmission(r.pool.QueryRow(ctx, `
		UPDATE booking.admissions
		   SET status = 'ACTIVE', claimed_at = $2
		 WHERE id = $1::uuid AND status = 'RESERVED' AND expires_at > $2
		 RETURNING `+admissionCols, jti, at))
	if errors.Is(err, pgx.ErrNoRows) || pkgpg.IsCode(err, "22P02") {
		return entity.Admission{}, inbound.ErrAdmissionToken
	}
	return mapAdmissionErr(a, err)
}

// ReserveBatch reserves one admission per entry in a single transaction (invariant #2).
func (r *AdmissionRepo) ReserveBatch(ctx context.Context, eventID string, entries []outbound.ReserveEntry, at time.Time) ([]outbound.ReserveOutcome, error) {
	out := make([]outbound.ReserveOutcome, 0, len(entries))
	err := pkgpg.WithTx(ctx, r.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		for _, e := range entries {
			out = append(out, r.reserveOne(ctx, tx, eventID, e, at))
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("admissions: reserve batch: %w", err)
	}
	return out, nil
}

func (r *AdmissionRepo) reserveOne(ctx context.Context, tx pgx.Tx, eventID string, e outbound.ReserveEntry, at time.Time) outbound.ReserveOutcome {
	res := outbound.ReserveOutcome{QueueEntryID: e.QueueEntryID, Units: e.Units}
	// Use a savepoint so a per-entry failure (e.g. anti-calo conflict) does not abort the whole batch.
	if _, err := tx.Exec(ctx, "SAVEPOINT entry"); err != nil {
		res.Outcome = "NO_SUPPLY_NOW"
		return res
	}
	if _, err := tx.Exec(ctx, `
		UPDATE booking.event_supply
		   SET allocated_units = allocated_units + $2, version = version + 1, updated_at = $3
		 WHERE event_id = $1::uuid`, eventID, e.Units, at); err != nil {
		_, _ = tx.Exec(ctx, "ROLLBACK TO SAVEPOINT entry")
		res.Outcome = supplyOutcome(err)
		return res
	}
	var allocationID string
	var expiresAt time.Time
	err := tx.QueryRow(ctx, `
		INSERT INTO booking.admissions (
			event_id, queue_entry_id, queue_seq, visitor_id, buyer_nik_hash, buyer_email, buyer_nik_masked,
			units, status, admission_jti, attendee_checks, edit_count,
			cfg_selection_ttl_seconds, cfg_payment_ttl_seconds, cfg_submit_grace_seconds,
			cfg_edit_enabled, cfg_max_edits, cfg_allow_mixed_categories,
			reserved_at, expires_at, account_id)
		SELECT $1::uuid, $2::uuid, $3, $4::uuid, $5, $6, $7,
			$8, 'RESERVED', $9::uuid, 0, 0,
			ev.selection_ttl_seconds, ev.payment_ttl_seconds, ev.selection_submit_grace_seconds,
			ev.selection_edit_enabled, ev.max_selection_edits, ev.allow_mixed_categories,
			$10::timestamptz, $10::timestamptz + make_interval(secs => ev.claim_window_seconds), NULLIF($11,'')::uuid
		  FROM booking.events ev WHERE ev.id = $1::uuid
		RETURNING id::text, expires_at`,
		eventID, e.QueueEntryID, e.Seq, e.VisitorID, e.BuyerNIKHash, e.BuyerEmail, e.BuyerNIKMasked,
		e.Units, e.AdmissionJTI, at, e.AccountID).Scan(&allocationID, &expiresAt)
	if err != nil {
		_, _ = tx.Exec(ctx, "ROLLBACK TO SAVEPOINT entry")
		if pkgpg.IsCode(err, pkgpg.CodeUniqueViolation) {
			return r.existingOutcome(ctx, tx, eventID, e)
		}
		if pkgpg.IsCode(err, pkgpg.CodeCheckViolation) {
			res.Outcome = "SOLD_OUT"
			return res
		}
		res.Outcome = "NO_SUPPLY_NOW"
		return res
	}
	_, _ = tx.Exec(ctx, "RELEASE SAVEPOINT entry")
	res.Outcome = "GRANTED"
	res.AllocationID = allocationID
	d := expiresAt
	res.ClaimDeadline = &d
	return res
}

// existingOutcome resolves a unique-violation to the right idempotent outcome.
func (r *AdmissionRepo) existingOutcome(ctx context.Context, tx pgx.Tx, eventID string, e outbound.ReserveEntry) outbound.ReserveOutcome {
	res := outbound.ReserveOutcome{QueueEntryID: e.QueueEntryID, Units: e.Units}
	var id string
	err := tx.QueryRow(ctx, `SELECT id::text FROM booking.admissions WHERE queue_entry_id = $1::uuid`, e.QueueEntryID).Scan(&id)
	if err == nil {
		res.Outcome = "ALREADY_ACTIVE"
		res.AllocationID = id
		return res
	}
	res.Outcome = "ALREADY_ACTIVE"
	return res
}

func supplyOutcome(err error) string {
	if pkgpg.IsCode(err, pkgpg.CodeCheckViolation) {
		return "SOLD_OUT"
	}
	return "NO_SUPPLY_NOW"
}

// Release compensates a saga: RELEASED + allocated_units -= units.
func (r *AdmissionRepo) Release(ctx context.Context, allocationID string) (bool, error) {
	var released bool
	err := pkgpg.WithTx(ctx, r.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		var eventID string
		var units int
		var status vo.AdmissionStatus
		err := tx.QueryRow(ctx, `SELECT event_id::text, units, status FROM booking.admissions WHERE id = $1::uuid FOR UPDATE`, allocationID).
			Scan(&eventID, &units, &status)
		if errors.Is(err, pgx.ErrNoRows) {
			return inbound.ErrNotFound
		}
		if err != nil {
			return err
		}
		if !status.IsActive() {
			return nil // already ended; idempotent
		}
		if _, err := tx.Exec(ctx, `
			UPDATE booking.admissions SET status='RELEASED', ended_at=now(), end_reason='UNCLAIMED'
			 WHERE id = $1::uuid`, allocationID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE booking.event_supply SET allocated_units = allocated_units - $2, version = version + 1, updated_at = now()
			 WHERE event_id = $1::uuid`, eventID, units); err != nil {
			return err
		}
		released = true
		return nil
	})
	return released, err
}

// DueSessions scans admissions whose expiry lapsed.
func (r *AdmissionRepo) DueSessions(ctx context.Context, now time.Time, limit int) ([]entity.Admission, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+admissionCols+`
		FROM booking.admissions
		WHERE status IN ('RESERVED','ACTIVE','EDITABLE') AND expires_at < $1
		ORDER BY expires_at LIMIT $2`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("admissions: due: %w", err)
	}
	defer rows.Close()
	var out []entity.Admission
	for rows.Next() {
		a, err := scanAdmission(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ExpireSession releases one admission's allocation in its own transaction. Held stock tied to a
// booking is released by the expiry sweeper via the unit of work; here we release the allocation.
func (r *AdmissionRepo) ExpireSession(ctx context.Context, admissionID string, now time.Time) error {
	return pkgpg.WithTx(ctx, r.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		var status vo.AdmissionStatus
		var eventID, queueEntryID string
		var units int
		if err := tx.QueryRow(ctx, `SELECT status, event_id::text, queue_entry_id::text, units
			FROM booking.admissions WHERE id=$1::uuid FOR UPDATE`, admissionID).
			Scan(&status, &eventID, &queueEntryID, &units); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return err
		}
		if !status.IsActive() {
			return nil // already terminal, idempotent
		}

		endReason := "SESSION_EXPIRED"
		outcome := "EXPIRED"
		if status == vo.AdmissionReserved {
			endReason = "UNCLAIMED"
			outcome = "UNCLAIMED"
		}

		// Release allocated stock.
		// RESERVED and ACTIVE: stock is in allocated_units (no booking hold exists yet).
		// EDITABLE: stock is in held_units, released by booking expiry sweeper via UoW — skip here.
		if status == vo.AdmissionReserved || status == vo.AdmissionActive {
			if _, err := tx.Exec(ctx, `
				UPDATE booking.event_supply
				   SET allocated_units = allocated_units - $2, version = version + 1, updated_at = $3
				 WHERE event_id = $1::uuid`, eventID, units, now); err != nil {
				return err
			}
		}

		if _, err := tx.Exec(ctx, `
			UPDATE booking.admissions SET status = 'EXPIRED', ended_at = $2, end_reason = $3
			 WHERE id = $1::uuid`, admissionID, now, endReason); err != nil {
			return err
		}

		// Publish journey.ended so Queue releases the slot (booking 10.R1, K7).
		payload, err := json.Marshal(map[string]any{
			"eventId": eventID, "queueEntryId": queueEntryID, "outcome": outcome,
		})
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO booking.outbox_events (routing_key, aggregate_id, schema_version, payload)
			VALUES ('booking.journey.ended', $1::uuid, 1, $2)`, admissionID, payload)
		return err
	})
}

func mapAdmissionErr(a entity.Admission, err error) (entity.Admission, error) {
	switch {
	case errors.Is(err, pgx.ErrNoRows), pkgpg.IsCode(err, "22P02"):
		return entity.Admission{}, inbound.ErrNotFound
	case err != nil:
		return entity.Admission{}, fmt.Errorf("admissions: %w", err)
	}
	return a, nil
}
