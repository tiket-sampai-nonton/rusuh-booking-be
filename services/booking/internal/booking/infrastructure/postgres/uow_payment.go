package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	domevent "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/event"
	vo "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/valueobject"
	inbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

// PaymentLock stamps payment_started_at, locking the order against edits before a payment attempt (v4.4).
// It is idempotent: a booking already stamped returns OK with no new event. A VERIFYING booking without a
// stamp is stamped like a PENDING_PAYMENT one (P1-5). Returns PAYMENT_EXPIRED when the window lapsed and
// BOOKING_NOT_PAYABLE when the booking is in any non-payable state. Retries on event-store concurrency (max 3).
func (u *UnitOfWork) PaymentLock(ctx context.Context, spec outbound.PaymentLockSpec) (outbound.PaymentLockResult, error) {
	return inTx(ctx, u, func(tx pgx.Tx) (outbound.PaymentLockResult, error) { return u.paymentLock(ctx, tx, spec) })
}

func (u *UnitOfWork) paymentLock(ctx context.Context, tx pgx.Tx, spec outbound.PaymentLockSpec) (outbound.PaymentLockResult, error) {
	// Step 1: plain read — discover eventID and admissionID (no lock yet, §9.0).
	eventID, admissionID, err := bookingRefs(ctx, tx, spec.BookingID)
	if err != nil {
		return outbound.PaymentLockResult{}, err
	}

	// Step 2: lock admission first (§9.0: admissions before bookings).
	// Read status + units to decide whether to release surplus allocated units (§9.4).
	var admStatus string
	var admUnits int
	if err := tx.QueryRow(ctx, `
		SELECT status, outstanding_units FROM booking.admissions WHERE id=$1::uuid FOR UPDATE`, admissionID).
		Scan(&admStatus, &admUnits); err != nil {
		return outbound.PaymentLockResult{}, err
	}

	// Step 3: lock booking row and read full state.
	var status vo.BookingStatus
	var version int
	var amountMinor int64
	var paymentExpiresAt time.Time
	var paymentStarted *time.Time
	if err := tx.QueryRow(ctx, `
		SELECT status, version, amount_minor, payment_expires_at, payment_started_at
		  FROM booking.bookings WHERE id=$1::uuid FOR UPDATE`, spec.BookingID).
		Scan(&status, &version, &amountMinor, &paymentExpiresAt, &paymentStarted); err != nil {
		return outbound.PaymentLockResult{}, err
	}

	now := clockOr(spec.Now)
	// Payment window lapsed: not lockable regardless of status.
	if now.After(paymentExpiresAt) {
		return outbound.PaymentLockResult{Outcome: "PAYMENT_EXPIRED", AmountMinor: amountMinor, PaymentExpiresAt: paymentExpiresAt}, nil
	}
	if !status.IsPayable() {
		return outbound.PaymentLockResult{Outcome: "BOOKING_NOT_PAYABLE", AmountMinor: amountMinor, PaymentExpiresAt: paymentExpiresAt}, nil
	}
	// Idempotent: already locked by a previous call (payment_started_at marks the lock; status is not the marker).
	if paymentStarted != nil {
		return outbound.PaymentLockResult{Outcome: "OK", Version: version, AmountMinor: amountMinor, PaymentExpiresAt: paymentExpiresAt}, nil
	}

	// Step 4: stamp payment_started_at (booking status stays PENDING_PAYMENT per K1/Δ1).
	if _, err := tx.Exec(ctx, `
		UPDATE booking.bookings
		   SET payment_started_at = COALESCE(payment_started_at, $2),
		       version = $3,
		       updated_at = $2
		 WHERE id = $1::uuid`, spec.BookingID, now, version+1); err != nil {
		return outbound.PaymentLockResult{}, err
	}

	// Step 5 (§9.4): if the admission is still EDITABLE, convert it. Unused units were already returned to
	// free stock at hold (outstanding_units = 0); a legacy row with a leftover counter releases it here.
	if admStatus == "EDITABLE" {
		if admUnits > 0 {
			if _, err := tx.Exec(ctx, `
				UPDATE booking.event_supply
				   SET allocated_units = allocated_units - $2, version = version + 1, updated_at = now()
				 WHERE event_id = $1::uuid`, eventID, admUnits); err != nil {
				return outbound.PaymentLockResult{}, err
			}
		}
		if _, err := tx.Exec(ctx, `
			UPDATE booking.admissions
			   SET status = 'CONVERTED', outstanding_units = 0, lock_reason = 'PAYMENT_STARTED',
			       locked_at = now(), ended_at = now(), end_reason = 'CONVERTED'
			 WHERE id = $1::uuid AND status = 'EDITABLE'`, admissionID); err != nil {
			return outbound.PaymentLockResult{}, err
		}
	}

	// Step 6: append BookingLocked event (audit trail for payment_started_at and admission conversion).
	locked := domevent.BookingLocked{BookingID: spec.BookingID, Reason: "PAYMENT_STARTED"}
	if err := u.appendEvent(ctx, tx, spec.BookingID, version, domevent.TypeBookingLocked, locked, spec.CorrelationID); err != nil {
		return outbound.PaymentLockResult{}, err
	}

	return outbound.PaymentLockResult{
		Outcome:          "OK",
		Version:          version + 1,
		AmountMinor:      amountMinor,
		PaymentExpiresAt: paymentExpiresAt,
	}, nil
}

// ExtendDeadline extends payment_expires_at once per booking, only while it is PENDING_PAYMENT and before the
// deadline (D-B3). The extension is recorded as a BookingDeadlineExtended event in the same transaction.
func (u *UnitOfWork) ExtendDeadline(ctx context.Context, spec outbound.DeadlineSpec) error {
	return u.retry(ctx, func(tx pgx.Tx) error {
		return u.extendDeadline(ctx, tx, spec)
	})
}

func (u *UnitOfWork) extendDeadline(ctx context.Context, tx pgx.Tx, spec outbound.DeadlineSpec) error {
	// Step 1: lock the booking and read the state each refusal depends on.
	var status vo.BookingStatus
	var version int
	var paymentExpiresAt time.Time
	var extendedAt *time.Time
	if err := tx.QueryRow(ctx, `
		SELECT status, version, payment_expires_at, deadline_extended_at
		  FROM booking.bookings WHERE id=$1::uuid FOR UPDATE`, spec.BookingID).
		Scan(&status, &version, &paymentExpiresAt, &extendedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return inbound.ErrNotFound
		}
		return err
	}
	if extendedAt != nil {
		return inbound.ErrDeadlineAlreadyExtended
	}
	if status != vo.BookingPendingPayment {
		return inbound.ErrNotPayable
	}
	if !time.Now().UTC().Before(paymentExpiresAt) {
		return inbound.ErrPaymentExpired
	}

	// Step 2: extend the deadline and advance the read model to the next stream version.
	if _, err := tx.Exec(ctx, `
		UPDATE booking.bookings
		   SET payment_expires_at = payment_expires_at + make_interval(secs => $2),
		       deadline_extended_at = now(),
		       version = version + 1,
		       updated_at = now()
		 WHERE id = $1::uuid`, spec.BookingID, spec.Seconds); err != nil {
		return err
	}
	// Step 3: append the audit event (no PII: the payload carries only the booking id, seconds and reason).
	// The metadata correlation id is the booking id, as before the split (spec.CorrelationID is not used here).
	extended := domevent.BookingDeadlineExtended{BookingID: spec.BookingID, Seconds: spec.Seconds, Reason: spec.Reason}
	return u.appendEvent(ctx, tx, spec.BookingID, version, domevent.TypeBookingDeadlineExtended, extended, spec.BookingID)
}

// StartVerifying is Phase A of the expiry sweeper: PENDING_PAYMENT past deadline → VERIFYING (design §7.2).
func (u *UnitOfWork) StartVerifying(ctx context.Context, bookingID string, verifyGraceSeconds int) (bool, error) {
	return inTx(ctx, u, func(tx pgx.Tx) (bool, error) { return u.startVerifying(ctx, tx, bookingID, verifyGraceSeconds) })
}

func (u *UnitOfWork) startVerifying(ctx context.Context, tx pgx.Tx, bookingID string, verifyGraceSeconds int) (bool, error) {
	var status vo.BookingStatus
	var version int
	var paymentExpiresAt time.Time
	err := tx.QueryRow(ctx, `SELECT status, version, payment_expires_at FROM booking.bookings WHERE id=$1::uuid FOR UPDATE`, bookingID).
		Scan(&status, &version, &paymentExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, inbound.ErrNotFound
	}
	if err != nil {
		return false, err
	}
	if status != vo.BookingPendingPayment {
		return false, nil // already VERIFYING or terminal — idempotent no-op
	}
	if time.Now().UTC().Before(paymentExpiresAt) {
		return false, nil // not yet past deadline
	}
	verificationUntil := time.Now().UTC().Add(time.Duration(verifyGraceSeconds) * time.Second)
	if err := u.appendEvent(ctx, tx, bookingID, version, domevent.TypePaymentVerifying,
		domevent.PaymentVerifying{BookingID: bookingID}, bookingID); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE booking.bookings SET status='VERIFYING', version=$2, verification_until=$3, updated_at=now() WHERE id=$1::uuid`,
		bookingID, version+1, verificationUntil); err != nil {
		return false, err
	}
	return true, nil
}
