package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	domevent "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/event"
	vo "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/valueobject"
	inbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

// Expire releases a booking's held stock (EXPIRED) or marks it REVIEW (payment found, stock held).
func (u *UnitOfWork) Expire(ctx context.Context, spec outbound.ExpireSpec) (bool, error) {
	return inTx(ctx, u, func(tx pgx.Tx) (bool, error) { return u.expire(ctx, tx, spec) })
}

func (u *UnitOfWork) expire(ctx context.Context, tx pgx.Tx, spec outbound.ExpireSpec) (bool, error) {
	// Step 1: plain read — discover eventID and admissionID (no lock yet, §9.0).
	eventID, admissionID, err := bookingRefs(ctx, tx, spec.BookingID)
	if err != nil {
		return false, err
	}

	// Step 2: lock admission first (§9.0: admissions before bookings).
	var admStatus string
	if err := tx.QueryRow(ctx, `
		SELECT status FROM booking.admissions WHERE id=$1::uuid FOR UPDATE`, admissionID).
		Scan(&admStatus); err != nil {
		return false, err
	}

	// Step 3: lock booking row and read full state.
	var status vo.BookingStatus
	var version int
	var publicRef, email string
	var buyerHash []byte
	var amount int64
	var paymentStarted *time.Time
	if err := tx.QueryRow(ctx, `
		SELECT status, version, public_ref, COALESCE(buyer_email_enc, ''), buyer_nik_hash, amount_minor, payment_started_at
		  FROM booking.bookings WHERE id=$1::uuid FOR UPDATE`, spec.BookingID).
		Scan(&status, &version, &publicRef, &email, &buyerHash, &amount, &paymentStarted); err != nil {
		return false, err
	}
	if !status.IsPayable() {
		return false, nil // already terminal — idempotent
	}
	// P1-4: a cancel after payment-lock is refused here, under the booking row lock, before any stock write.
	if spec.Reason == "CANCELLED" && paymentStarted != nil {
		return false, inbound.ErrBookingLocked
	}

	// Step 4: reads that need no supply lock (the booking and the admission are locked): the HELD units per category
	// and the queue_entry_id for the journey.ended payload.
	byCat, err := heldByCategory(ctx, tx, spec.BookingID)
	if err != nil {
		return false, err
	}
	var queueEntryID string
	if err := tx.QueryRow(ctx, `
		SELECT queue_entry_id::text FROM booking.admissions WHERE id=$1::uuid`, admissionID).
		Scan(&queueEntryID); err != nil {
		return false, err
	}

	// Step 5: lock event_supply (§9.0: after booking, before quota/categories).
	if err := lockEventSupply(ctx, tx, eventID); err != nil {
		return false, err
	}

	// Step 6: the event and the booking's final status. REVIEW: late payment found — mark REVIEW and give the held
	// stock back (P0-5). Otherwise EXPIRED (NOT_PAID / NO_ATTEMPT / grace exhausted), or CANCELLED for a cancel.
	var outbox []outboxRow
	if spec.Review {
		review := domevent.BookingReview{BookingID: spec.BookingID, AmountMinor: amount}
		if err := u.appendEvent(ctx, tx, spec.BookingID, version, domevent.TypeBookingReview, review, spec.CorrelationID); err != nil {
			return false, err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE booking.bookings SET status='REVIEW', version=$2, updated_at=now()
			 WHERE id=$1::uuid`, spec.BookingID, version+1); err != nil {
			return false, err
		}
		outbox = []outboxRow{
			reviewRow(spec.BookingID, email, amount),
			journeyEndedRow(spec.BookingID, eventID, queueEntryID, "EXPIRED"),
		}
	} else {
		expired := domevent.BookingExpired{BookingID: spec.BookingID, Reason: spec.Reason}
		if err := u.appendEvent(ctx, tx, spec.BookingID, version, domevent.TypeBookingExpired, expired, spec.CorrelationID); err != nil {
			return false, err
		}
		finalStatus := "EXPIRED"
		if spec.Reason == "CANCELLED" {
			finalStatus = "CANCELLED"
		}
		if _, err := tx.Exec(ctx, `
			UPDATE booking.bookings SET status=$3, ended_at=now(), version=$2, updated_at=now()
			 WHERE id=$1::uuid`, spec.BookingID, version+1, finalStatus); err != nil {
			return false, err
		}
		msg := map[string]any{"bookingId": spec.BookingID, "publicRef": publicRef, "buyerEmailEnc": email, "reason": spec.Reason}
		outbox = []outboxRow{
			{"booking.expired", spec.BookingID, msg},
			journeyEndedRow(spec.BookingID, eventID, queueEntryID, finalStatus),
		}
	}

	// Step 7: give the held stock back and, §7.2 / §9.6, convert an EDITABLE admission on any terminal booking path.
	if err := releaseHeldItems(ctx, tx, spec.BookingID, eventID, buyerHash, byCat); err != nil {
		return false, err
	}
	if admStatus == "EDITABLE" {
		if err := endEditableAdmission(ctx, tx, eventID, admissionID); err != nil {
			return false, err
		}
	}
	return true, enqueueOutboxMany(ctx, tx, outbox)
}
