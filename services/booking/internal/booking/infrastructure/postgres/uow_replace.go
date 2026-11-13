package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	domevent "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/event"
	inbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

// ReplaceItems performs an atomic edit without resetting the payment deadline.
func (u *UnitOfWork) ReplaceItems(ctx context.Context, spec outbound.ReplaceSpec) (int, error) {
	return inTx(ctx, u, func(tx pgx.Tx) (int, error) { return u.replace(ctx, tx, spec) })
}

func (u *UnitOfWork) replace(ctx context.Context, tx pgx.Tx, spec outbound.ReplaceSpec) (int, error) {
	// Design §9.3 Step 2: lock admission first — must be EDITABLE, within grace, under edit limit.
	if err := lockEditableAdmission(ctx, tx, spec.AdmissionID, clockOr(spec.Now)); err != nil {
		return 0, err
	}

	version, err := lockEditableBooking(ctx, tx, spec.BookingID, spec.ExpectedVersion)
	if err != nil {
		return 0, err
	}

	// Lock order aligned with hold (admission → booking → supply → quota → category): an increase needs free
	// stock from the supply row, so it is locked before the quota and category rows.
	if err := lockEventSupply(ctx, tx, spec.EventID); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `SELECT 1 FROM booking.buyer_quotas WHERE event_id=$1::uuid AND buyer_nik_hash=$2 FOR UPDATE`,
		spec.EventID, spec.BuyerNIKHash); err != nil {
		return 0, err
	}

	// Lock the categories the booking holds now and the ones the new lines add.
	oldByCat, err := heldByCategory(ctx, tx, spec.BookingID)
	if err != nil {
		return 0, err
	}
	for cat := range oldByCat {
		if err := lockCategory(ctx, tx, cat); err != nil {
			return 0, err
		}
	}
	newByCat := map[string]int{}
	for _, l := range spec.Lines {
		newByCat[l.CategoryID]++
		if _, ok := oldByCat[l.CategoryID]; !ok {
			if err := lockCategory(ctx, tx, l.CategoryID); err != nil {
				return 0, err
			}
		}
	}

	replaced := domevent.BookingItemsReplaced{
		BookingID: spec.BookingID, AmountMinor: spec.NewAmountMinor, ItemQuantity: len(spec.Lines), Items: itemSnapshots(spec.Lines),
	}
	if err := u.appendEvent(ctx, tx, spec.BookingID, version, domevent.TypeBookingItemsReplaced, replaced, spec.CorrelationID); err != nil {
		return 0, err
	}

	if err := replaceItemRows(ctx, tx, spec); err != nil {
		return 0, err
	}
	if err := adjustReplaceCounters(ctx, tx, spec, oldByCat, newByCat); err != nil {
		return 0, err
	}

	newVersion := version + 1
	if _, err := tx.Exec(ctx, `UPDATE booking.bookings SET version=$2, item_quantity=$3, amount_minor=$4, updated_at=now() WHERE id=$1::uuid`,
		spec.BookingID, newVersion, len(spec.Lines), spec.NewAmountMinor); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `UPDATE booking.admissions SET edit_count=edit_count+1 WHERE id=$1::uuid`, spec.AdmissionID); err != nil {
		return 0, mapEditErr(err)
	}
	return newVersion, nil
}

// lockEditableAdmission locks the admission and refuses an edit unless it is EDITABLE, inside its grace window and
// under its edit limit. A CONVERTED admission means the booking is already locked.
func lockEditableAdmission(ctx context.Context, tx pgx.Tx, admissionID string, now time.Time) error {
	var status string
	var expiresAt time.Time
	var editCount, cfgMaxEdits, cfgGrace int
	if err := tx.QueryRow(ctx, `
		SELECT status, expires_at, edit_count, cfg_max_edits, cfg_submit_grace_seconds
		FROM booking.admissions WHERE id=$1::uuid FOR UPDATE`, admissionID).
		Scan(&status, &expiresAt, &editCount, &cfgMaxEdits, &cfgGrace); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return inbound.ErrNotFound
		}
		return err
	}
	if status == "CONVERTED" {
		return inbound.ErrBookingLocked
	}
	if status != "EDITABLE" {
		return inbound.ErrSessionExpired
	}
	if now.After(expiresAt.Add(time.Duration(cfgGrace) * time.Second)) {
		return inbound.ErrSessionExpired
	}
	if editCount >= cfgMaxEdits {
		return inbound.ErrEditLimit
	}
	return nil
}

// lockEditableBooking locks the booking row and returns its stream version. A booking whose payment started is
// locked against edits; a version other than expectedVersion is a concurrent edit.
func lockEditableBooking(ctx context.Context, tx pgx.Tx, bookingID string, expectedVersion int) (int, error) {
	var version int
	var paymentStarted *time.Time
	err := tx.QueryRow(ctx, `SELECT version, payment_started_at FROM booking.bookings WHERE id=$1::uuid FOR UPDATE`, bookingID).
		Scan(&version, &paymentStarted)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, inbound.ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	if paymentStarted != nil {
		return 0, inbound.ErrBookingLocked
	}
	if version != expectedVersion {
		return 0, inbound.ErrVersionConflict
	}
	return version, nil
}

// replaceItemRows marks the booking's HELD items REPLACED and inserts the new lines as HELD.
func replaceItemRows(ctx context.Context, tx pgx.Tx, spec outbound.ReplaceSpec) error {
	if _, err := tx.Exec(ctx, `UPDATE booking.booking_items SET item_status='REPLACED' WHERE booking_id=$1::uuid AND item_status='HELD'`, spec.BookingID); err != nil {
		return err
	}
	for _, l := range spec.Lines {
		if _, err := tx.Exec(ctx, `
			INSERT INTO booking.booking_items (booking_id, event_id, category_id, unit_price_minor,
				attendee_nik_hash, attendee_nik_enc, attendee_nik_masked, attendee_name, item_status)
			VALUES ($1::uuid,$2::uuid,$3::uuid,$4,$5,$6,$7,$8,'HELD')`,
			spec.BookingID, spec.EventID, l.CategoryID, l.UnitPriceMinor,
			l.AttendeeNIKHash, l.AttendeeNIKEnc, l.AttendeeNIKMasked, l.AttendeeName); err != nil {
			return mapHoldErr(err)
		}
	}
	return nil
}

// adjustReplaceCounters moves the held counters from the old items to the new ones: per category, then the supply and
// buyer quota by the net change in units.
func adjustReplaceCounters(ctx context.Context, tx pgx.Tx, spec outbound.ReplaceSpec, oldByCat, newByCat map[string]int) error {
	for cat := range union(oldByCat, newByCat) {
		delta := newByCat[cat] - oldByCat[cat]
		if delta == 0 {
			continue
		}
		if _, err := tx.Exec(ctx, `UPDATE booking.category_inventories SET held_quantity=held_quantity+$2, updated_at=now() WHERE category_id=$1::uuid`, cat, delta); err != nil {
			return mapHoldErr(err)
		}
	}
	supplyDelta := len(spec.Lines) - sum(oldByCat)
	if supplyDelta == 0 {
		return nil
	}
	// E1 (ADR-030, PROVISIONAL pending the human gate; E2 would refuse delta > 0): after hold the admission owns no
	// reserved units, so an edit moves only held_units. An increase is limited by free stock (the supply CHECK is the
	// backstop → CATEGORY_UNAVAILABLE) and a decrease returns units to free stock immediately.
	if _, err := tx.Exec(ctx, `UPDATE booking.event_supply SET held_units=held_units+$2, version=version+1, updated_at=now() WHERE event_id=$1::uuid`, spec.EventID, supplyDelta); err != nil {
		return mapHoldErr(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE booking.buyer_quotas SET held_quantity=held_quantity+$3, updated_at=now() WHERE event_id=$1::uuid AND buyer_nik_hash=$2`, spec.EventID, spec.BuyerNIKHash, supplyDelta); err != nil {
		return mapHoldErr(err)
	}
	return nil
}
