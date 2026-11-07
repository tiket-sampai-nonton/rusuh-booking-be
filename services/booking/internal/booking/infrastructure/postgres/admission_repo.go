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
	buyer_nik_hash, COALESCE(buyer_email_enc, ''), COALESCE(buyer_nik_masked, ''), units, status,
	COALESCE(admission_jti::text, ''), attendee_checks, edit_count, COALESCE(account_id::text, ''),
	cfg_selection_ttl_seconds, cfg_payment_ttl_seconds, cfg_submit_grace_seconds,
	cfg_edit_enabled, cfg_max_edits, cfg_allow_mixed_categories,
	reserved_at, expires_at, outstanding_units`

func scanAdmission(row pgx.Row) (entity.Admission, error) {
	var a entity.Admission
	err := row.Scan(
		&a.ID, &a.EventID, &a.QueueEntryID, &a.QueueSeq, &a.VisitorID,
		&a.BuyerNIKHash, &a.BuyerEmailEnc, &a.BuyerNIKMasked, &a.Units, &a.Status,
		&a.AdmissionJTI, &a.AttendeeChecks, &a.EditCount, &a.AccountID,
		&a.CfgSelectionTTLSeconds, &a.CfgPaymentTTLSeconds, &a.CfgSubmitGraceSeconds,
		&a.CfgEditEnabled, &a.CfgMaxEdits, &a.CfgAllowMixedCategories,
		&a.ReservedAt, &a.ExpiresAt, &a.OutstandingUnits,
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
	// Use a savepoint so a per-entry failure does not abort the whole batch transaction.
	if _, err := tx.Exec(ctx, "SAVEPOINT entry"); err != nil {
		res.Outcome = "NO_SUPPLY_NOW"
		return res
	}

	// Idempotent replay: an entry that already holds a reservation gets that same grant back, even when the
	// stock it holds is the reason nothing is free now (the Promotor may have failed to promote it last tick).
	var prevID, prevStatus string
	var prevUnits int
	var prevExpires time.Time
	if err := tx.QueryRow(ctx, `SELECT id::text, status, units, expires_at FROM booking.admissions WHERE queue_entry_id = $1::uuid`, e.QueueEntryID).
		Scan(&prevID, &prevStatus, &prevUnits, &prevExpires); err == nil {
		_, _ = tx.Exec(ctx, "RELEASE SAVEPOINT entry")
		res.AllocationID = prevID
		if prevStatus == "RESERVED" {
			res.Outcome, res.Units = "GRANTED", prevUnits
			res.ClaimDeadline = &prevExpires
		} else {
			res.Outcome = "ALREADY_ACTIVE"
		}
		return res
	}

	// Step 1 (§9.1): lock event_supply FOR UPDATE and read stock counters.
	var totalCap, allocUnits, heldUnits, soldUnits, maxPerOrder int
	if err := tx.QueryRow(ctx, `
		SELECT s.total_capacity, s.allocated_units, s.held_units, s.sold_units, ev.max_tickets_per_order
		  FROM booking.event_supply s
		  JOIN booking.events ev ON ev.id = s.event_id AND ev.published_at IS NOT NULL AND ev.status <> 'CANCELLED'
		 WHERE s.event_id=$1::uuid FOR UPDATE OF s`, eventID).
		Scan(&totalCap, &allocUnits, &heldUnits, &soldUnits, &maxPerOrder); err != nil {
		_, _ = tx.Exec(ctx, "ROLLBACK TO SAVEPOINT entry")
		res.Outcome = "NO_SUPPLY_NOW"
		return res
	}
	freeUnits := totalCap - allocUnits - heldUnits - soldUnits

	// Steps 2-3 (buyer_quotas upsert, remaining clamp) run only when the entry carries a buyer NIK. In the
	// anonymous flow (ADR-030) the buyer quota is checked at hold, so reserve never touches buyer_quotas.
	remaining := maxPerOrder
	if len(e.BuyerNIKHash) > 0 {
		if _, err := tx.Exec(ctx, `
			INSERT INTO booking.buyer_quotas (event_id, buyer_nik_hash, max_quantity)
			SELECT $1::uuid, $2, ev.max_tickets_per_buyer_nik
			  FROM booking.events ev WHERE ev.id=$1::uuid
			ON CONFLICT (event_id, buyer_nik_hash) DO NOTHING`, eventID, e.BuyerNIKHash); err != nil {
			_, _ = tx.Exec(ctx, "ROLLBACK TO SAVEPOINT entry")
			res.Outcome = "NO_SUPPLY_NOW"
			return res
		}
		var heldQ, paidQ, maxQ int
		if err := tx.QueryRow(ctx, `
			SELECT held_quantity, paid_quantity, max_quantity
			  FROM booking.buyer_quotas
			 WHERE event_id=$1::uuid AND buyer_nik_hash=$2 FOR UPDATE`, eventID, e.BuyerNIKHash).
			Scan(&heldQ, &paidQ, &maxQ); err != nil {
			_, _ = tx.Exec(ctx, "ROLLBACK TO SAVEPOINT entry")
			res.Outcome = "NO_SUPPLY_NOW"
			return res
		}
		remaining = maxQ - (heldQ + paidQ)
		if remaining <= 0 {
			_, _ = tx.Exec(ctx, "ROLLBACK TO SAVEPOINT entry")
			res.Outcome = "BUYER_QUOTA_EXHAUSTED"
			return res
		}
	}

	// Step 4 (§9.1): supply checks.
	if freeUnits+allocUnits+heldUnits == 0 {
		// Fully sold out (free=0, alloc=0, held=0): mark event SOLD_OUT and write outbox.
		_, _ = tx.Exec(ctx, `
			UPDATE booking.events SET status='SOLD_OUT'
			 WHERE id=$1::uuid AND status='AVAILABLE'`, eventID)
		_ = enqueueOutboxTx(ctx, tx, "booking.sold_out", eventID, map[string]any{"eventId": eventID})
		_, _ = tx.Exec(ctx, "ROLLBACK TO SAVEPOINT entry")
		res.Outcome = "SOLD_OUT"
		return res
	}
	if freeUnits == 0 {
		// free=0 but alloc+held > 0: stock is reserved but not yet held → temporary unavailability.
		_, _ = tx.Exec(ctx, "ROLLBACK TO SAVEPOINT entry")
		res.Outcome = "NO_SUPPLY_NOW"
		return res
	}

	// Step 5: compute units to allocate (min of what's free, what's requested, what's remaining on quota).
	units := e.Units
	if units > maxPerOrder {
		units = maxPerOrder
	}
	if units > remaining {
		units = remaining
	}
	if units > freeUnits {
		units = freeUnits
	}

	// Step 6: allocate in event_supply.
	if _, err := tx.Exec(ctx, `
		UPDATE booking.event_supply
		   SET allocated_units = allocated_units + $2, version = version + 1, updated_at = $3
		 WHERE event_id = $1::uuid`, eventID, units, at); err != nil {
		_, _ = tx.Exec(ctx, "ROLLBACK TO SAVEPOINT entry")
		res.Outcome = supplyOutcome(err)
		return res
	}

	// Step 7 (F4): mark SOLD_OUT only when nothing can come back: no other un-held reservation and no hold.
	// Taking the last free units while other admissions still hold reservations is not a sell-out.
	if freeUnits-units == 0 && allocUnits+heldUnits == 0 {
		_, _ = tx.Exec(ctx, `
			UPDATE booking.events SET status='SOLD_OUT'
			 WHERE id=$1::uuid AND status='AVAILABLE'`, eventID)
		_ = enqueueOutboxTx(ctx, tx, "booking.sold_out", eventID, map[string]any{"eventId": eventID})
	}

	// Step 8: insert admission row.
	var allocationID string
	var expiresAt time.Time
	err := tx.QueryRow(ctx, `
		INSERT INTO booking.admissions (
			event_id, queue_entry_id, queue_seq, visitor_id, buyer_nik_hash, buyer_email_enc, buyer_nik_masked,
			units, outstanding_units, status, admission_jti, attendee_checks, edit_count,
			cfg_selection_ttl_seconds, cfg_payment_ttl_seconds, cfg_submit_grace_seconds,
			cfg_edit_enabled, cfg_max_edits, cfg_allow_mixed_categories,
			reserved_at, expires_at, account_id)
		SELECT $1::uuid, $2::uuid, $3, $4::uuid, $5, NULLIF($6,''), NULLIF($7,''),
			$8, $8, 'RESERVED', $9::uuid, 0, 0,
			ev.selection_ttl_seconds, ev.payment_ttl_seconds, ev.selection_submit_grace_seconds,
			ev.selection_edit_enabled, ev.max_selection_edits, ev.allow_mixed_categories,
			$10::timestamptz, $10::timestamptz + make_interval(secs => ev.claim_window_seconds),
			NULLIF($11,'')::uuid
		  FROM booking.events ev WHERE ev.id = $1::uuid
		RETURNING id::text, expires_at`,
		eventID, e.QueueEntryID, e.Seq, e.VisitorID, e.BuyerNIKHash, e.BuyerEmailEnc, e.BuyerNIKMasked,
		units, e.AdmissionJTI, at, e.AccountID).Scan(&allocationID, &expiresAt)
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
	res.Units = units
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
		err := tx.QueryRow(ctx, `SELECT event_id::text, outstanding_units, status FROM booking.admissions WHERE id = $1::uuid FOR UPDATE`, allocationID).
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
			UPDATE booking.admissions SET status='RELEASED', outstanding_units=0, ended_at=now(), end_reason='UNCLAIMED'
			 WHERE id = $1::uuid`, allocationID); err != nil {
			return err
		}
		if units > 0 {
			if _, err := tx.Exec(ctx, `
				UPDATE booking.event_supply SET allocated_units = allocated_units - $2, version = version + 1, updated_at = now()
				 WHERE event_id = $1::uuid`, eventID, units); err != nil {
				return err
			}
		}
		released = true
		return nil
	})
	return released, err
}

// DueSessions scans admissions whose expiry lapsed, applying grace for ACTIVE/EDITABLE (design §7.1).
func (r *AdmissionRepo) DueSessions(ctx context.Context, now time.Time, limit int) ([]entity.Admission, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+admissionCols+`
		FROM booking.admissions
		WHERE status IN ('RESERVED','ACTIVE','EDITABLE')
		  AND expires_at + CASE
		        WHEN status = 'RESERVED' THEN interval '0'
		        ELSE (cfg_submit_grace_seconds || ' seconds')::interval
		      END < $1
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
		var outstanding int
		if err := tx.QueryRow(ctx, `SELECT status, event_id::text, queue_entry_id::text, outstanding_units
			FROM booking.admissions WHERE id=$1::uuid FOR UPDATE`, admissionID).
			Scan(&status, &eventID, &queueEntryID, &outstanding); err != nil {
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

		// Release only this admission's own still-reserved units (outstanding_units): the counter is 0 once
		// the hold consumed/returned them, so a second expiry or an EDITABLE session releases nothing twice.
		if outstanding > 0 {
			if _, err := tx.Exec(ctx, `
				UPDATE booking.event_supply
				   SET allocated_units = allocated_units - $2, version = version + 1, updated_at = $3
				 WHERE event_id = $1::uuid`, eventID, outstanding, now); err != nil {
				return err
			}
		}

		// CONVERTED requires lock_reason (admissions_check2), so it is set in the same statement.
		if status == vo.AdmissionEditable {
			// No journey.ended outbox: the booking is still PENDING_PAYMENT and its expiry sweeper publishes it later.
			_, err := tx.Exec(ctx, `
				UPDATE booking.admissions
				   SET status = 'CONVERTED', outstanding_units = 0, ended_at = $2, end_reason = $3, lock_reason = 'SELECTION_EXPIRED', locked_at = $2
				 WHERE id = $1::uuid`, admissionID, now, endReason)
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE booking.admissions SET status = 'EXPIRED', outstanding_units = 0, ended_at = $2, end_reason = $3 WHERE id = $1::uuid`,
			admissionID, now, endReason); err != nil {
			return err
		}

		// Publish journey.ended so Queue releases the slot (RESERVED/ACTIVE only).
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

// enqueueOutboxTx writes one outbox row inside an existing transaction (used by reserveOne).
func enqueueOutboxTx(ctx context.Context, tx pgx.Tx, routingKey, aggregateID string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO booking.outbox_events (routing_key, aggregate_id, schema_version, payload)
		VALUES ($1, $2::uuid, 1, $3)`, routingKey, aggregateID, raw)
	return err
}
