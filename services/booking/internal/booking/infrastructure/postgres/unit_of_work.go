package postgres

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/eventstore"
	pkgpg "github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/postgres"

	domevent "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/event"
	vo "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/valueobject"
	inbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

const maxRetries = 3

// UnitOfWork performs the stock/money commands in one transaction each, hiding the fixed lock order
// (supply → category → quota → booking), the event-store append, the read-model projection, and the
// outbox write. It satisfies outbound.IUnitOfWork.
type UnitOfWork struct {
	pool  *pgxpool.Pool
	store *eventstore.Store
}

// NewUnitOfWork constructs the unit of work over the pool and the booking_events store.
func NewUnitOfWork(pool *pgxpool.Pool) (*UnitOfWork, error) {
	st, err := eventstore.New("booking_events")
	if err != nil {
		return nil, err
	}
	return &UnitOfWork{pool: pool, store: st}, nil
}

// Hold performs the atomic hold (invariant #1). Retries on event-store concurrency (max 3).
func (u *UnitOfWork) Hold(ctx context.Context, spec outbound.HoldSpec) (outbound.HoldResult, error) {
	var res outbound.HoldResult
	err := u.retry(ctx, func(tx pgx.Tx) error {
		r, err := u.hold(ctx, tx, spec)
		res = r
		return err
	})
	return res, err
}

func (u *UnitOfWork) hold(ctx context.Context, tx pgx.Tx, spec outbound.HoldSpec) (outbound.HoldResult, error) {
	n := len(spec.Lines)

	// (1) SUPPLY first — serialises concurrent holds per event.
	if _, err := tx.Exec(ctx, `SELECT 1 FROM booking.event_supply WHERE event_id=$1::uuid FOR UPDATE`, spec.EventID); err != nil {
		return outbound.HoldResult{}, err
	}
	// (2) CATEGORY rows in deterministic id order.
	byCat := map[string]int{}
	for _, l := range spec.Lines {
		byCat[l.CategoryID]++
	}
	for cat := range byCat {
		if _, err := tx.Exec(ctx, `SELECT 1 FROM booking.category_inventories WHERE category_id=$1::uuid FOR UPDATE`, cat); err != nil {
			return outbound.HoldResult{}, err
		}
	}
	// (3) QUOTA — upsert then lock the buyer row.
	if _, err := tx.Exec(ctx, `
		INSERT INTO booking.buyer_quotas (event_id, buyer_nik_hash) VALUES ($1::uuid, $2)
		ON CONFLICT (event_id, buyer_nik_hash) DO NOTHING`, spec.EventID, spec.BuyerNIKHash); err != nil {
		return outbound.HoldResult{}, err
	}
	if _, err := tx.Exec(ctx, `SELECT 1 FROM booking.buyer_quotas WHERE event_id=$1::uuid AND buyer_nik_hash=$2 FOR UPDATE`,
		spec.EventID, spec.BuyerNIKHash); err != nil {
		return outbound.HoldResult{}, err
	}

	// (4) append event store (optimistic).
	payload := domevent.BookingHeld{
		BookingID: spec.BookingID, EventID: spec.EventID, AdmissionID: spec.AdmissionID,
		AmountMinor: spec.AmountMinor, ItemQuantity: n, Items: itemSnapshots(spec.Lines),
	}
	if _, err := u.store.Append(ctx, tx, spec.BookingID, 0, eventstore.NewEvent{
		Type: domevent.TypeBookingHeld, SchemaVersion: 1, Payload: payload, Metadata: meta(spec.CorrelationID),
	}); err != nil {
		return outbound.HoldResult{}, err
	}

	// (5) project read model (booking LAST).
	if _, err := tx.Exec(ctx, `
		INSERT INTO booking.bookings (id, public_ref, event_id, admission_id, visitor_id, buyer_nik_hash,
			buyer_email, status, amount_minor, item_quantity, payment_expires_at, version, account_id)
		VALUES ($1::uuid,$2,$3::uuid,$4::uuid,$5::uuid,$6,$7,'PENDING_PAYMENT',$8,$9,$10,1, NULLIF($11,'')::uuid)`,
		spec.BookingID, spec.PublicRef, spec.EventID, spec.AdmissionID, spec.VisitorID, spec.BuyerNIKHash,
		spec.BuyerEmail, spec.AmountMinor, n, spec.PaymentExpiresAt, spec.AccountID); err != nil {
		return outbound.HoldResult{}, mapHoldErr(err)
	}
	for _, l := range spec.Lines {
		if _, err := tx.Exec(ctx, `
			INSERT INTO booking.booking_items (booking_id, event_id, category_id, unit_price_minor,
				attendee_nik_hash, attendee_nik_enc, attendee_nik_masked, attendee_name, item_status)
			VALUES ($1::uuid,$2::uuid,$3::uuid,$4,$5,$6,$7,$8,'HELD')`,
			spec.BookingID, spec.EventID, l.CategoryID, l.UnitPriceMinor,
			l.AttendeeNIKHash, l.AttendeeNIKEnc, l.AttendeeNIKMasked, l.AttendeeName); err != nil {
			return outbound.HoldResult{}, mapHoldErr(err)
		}
	}

	// (6) bump counters — anti-oversell CHECKs validated here.
	for cat, cn := range byCat {
		if _, err := tx.Exec(ctx, `UPDATE booking.category_inventories SET held_quantity=held_quantity+$2, updated_at=now() WHERE category_id=$1::uuid`, cat, cn); err != nil {
			return outbound.HoldResult{}, mapHoldErr(err)
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE booking.event_supply SET held_units=held_units+$2, version=version+1, updated_at=now() WHERE event_id=$1::uuid`, spec.EventID, n); err != nil {
		return outbound.HoldResult{}, mapHoldErr(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE booking.buyer_quotas SET held_quantity=held_quantity+$3, updated_at=now() WHERE event_id=$1::uuid AND buyer_nik_hash=$2`, spec.EventID, spec.BuyerNIKHash, n); err != nil {
		return outbound.HoldResult{}, mapHoldErr(err)
	}

	return outbound.HoldResult{
		BookingID: spec.BookingID, PublicRef: spec.PublicRef, Version: 1,
		Status: string(vo.BookingPendingPayment), AmountMinor: spec.AmountMinor, PaymentExpiresAt: spec.PaymentExpiresAt,
	}, nil
}

// ReplaceItems performs an atomic edit without resetting the payment deadline.
func (u *UnitOfWork) ReplaceItems(ctx context.Context, spec outbound.ReplaceSpec) (int, error) {
	var newVersion int
	err := u.retry(ctx, func(tx pgx.Tx) error {
		v, err := u.replace(ctx, tx, spec)
		newVersion = v
		return err
	})
	return newVersion, err
}

func (u *UnitOfWork) replace(ctx context.Context, tx pgx.Tx, spec outbound.ReplaceSpec) (int, error) {
	var version int
	var paymentStarted *time.Time
	err := tx.QueryRow(ctx, `SELECT version, payment_started_at FROM booking.bookings WHERE id=$1::uuid FOR UPDATE`, spec.BookingID).
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
	if version != spec.ExpectedVersion {
		return 0, inbound.ErrVersionConflict
	}

	// Release old held units per category before inserting the new ones.
	oldByCat, err := heldByCategory(ctx, tx, spec.BookingID)
	if err != nil {
		return 0, err
	}
	for cat := range oldByCat {
		if _, err := tx.Exec(ctx, `SELECT 1 FROM booking.category_inventories WHERE category_id=$1::uuid FOR UPDATE`, cat); err != nil {
			return 0, err
		}
	}
	newByCat := map[string]int{}
	for _, l := range spec.Lines {
		newByCat[l.CategoryID]++
		if _, ok := oldByCat[l.CategoryID]; !ok {
			if _, err := tx.Exec(ctx, `SELECT 1 FROM booking.category_inventories WHERE category_id=$1::uuid FOR UPDATE`, l.CategoryID); err != nil {
				return 0, err
			}
		}
	}

	payload := domevent.BookingItemsReplaced{
		BookingID: spec.BookingID, AmountMinor: spec.NewAmountMinor, ItemQuantity: len(spec.Lines), Items: itemSnapshots(spec.Lines),
	}
	if _, err := u.store.Append(ctx, tx, spec.BookingID, version, eventstore.NewEvent{
		Type: domevent.TypeBookingItemsReplaced, SchemaVersion: 1, Payload: payload, Metadata: meta(spec.CorrelationID),
	}); err != nil {
		return 0, err
	}

	if _, err := tx.Exec(ctx, `UPDATE booking.booking_items SET item_status='REPLACED' WHERE booking_id=$1::uuid AND item_status='HELD'`, spec.BookingID); err != nil {
		return 0, err
	}
	for _, l := range spec.Lines {
		if _, err := tx.Exec(ctx, `
			INSERT INTO booking.booking_items (booking_id, event_id, category_id, unit_price_minor,
				attendee_nik_hash, attendee_nik_enc, attendee_nik_masked, attendee_name, item_status)
			VALUES ($1::uuid,$2::uuid,$3::uuid,$4,$5,$6,$7,$8,'HELD')`,
			spec.BookingID, spec.EventID, l.CategoryID, l.UnitPriceMinor,
			l.AttendeeNIKHash, l.AttendeeNIKEnc, l.AttendeeNIKMasked, l.AttendeeName); err != nil {
			return 0, mapHoldErr(err)
		}
	}
	// Adjust held counters per category.
	for cat := range union(oldByCat, newByCat) {
		delta := newByCat[cat] - oldByCat[cat]
		if delta == 0 {
			continue
		}
		if _, err := tx.Exec(ctx, `UPDATE booking.category_inventories SET held_quantity=held_quantity+$2, updated_at=now() WHERE category_id=$1::uuid`, cat, delta); err != nil {
			return 0, mapHoldErr(err)
		}
	}
	supplyDelta := len(spec.Lines) - sum(oldByCat)
	if supplyDelta != 0 {
		if _, err := tx.Exec(ctx, `UPDATE booking.event_supply SET held_units=held_units+$2, version=version+1, updated_at=now() WHERE event_id=$1::uuid`, spec.EventID, supplyDelta); err != nil {
			return 0, mapHoldErr(err)
		}
		if _, err := tx.Exec(ctx, `UPDATE booking.buyer_quotas SET held_quantity=held_quantity+$3, updated_at=now() WHERE event_id=$1::uuid AND buyer_nik_hash=$2`, spec.EventID, spec.BuyerNIKHash, supplyDelta); err != nil {
			return 0, mapHoldErr(err)
		}
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

// Settle moves held → sold and issues tickets, idempotently (guard status != PAID).
func (u *UnitOfWork) Settle(ctx context.Context, spec outbound.SettleSpec) (bool, error) {
	var settled bool
	err := u.retry(ctx, func(tx pgx.Tx) error {
		s, err := u.settle(ctx, tx, spec)
		settled = s
		return err
	})
	return settled, err
}

func (u *UnitOfWork) settle(ctx context.Context, tx pgx.Tx, spec outbound.SettleSpec) (bool, error) {
	var status vo.BookingStatus
	var version int
	var eventID string
	var buyerHash []byte
	var amount int64
	var publicRef, email string
	err := tx.QueryRow(ctx, `SELECT status, version, event_id::text, buyer_nik_hash, amount_minor, public_ref, buyer_email
		FROM booking.bookings WHERE id=$1::uuid FOR UPDATE`, spec.BookingID).
		Scan(&status, &version, &eventID, &buyerHash, &amount, &publicRef, &email)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, inbound.ErrNotFound
	}
	if err != nil {
		return false, err
	}
	if status == vo.BookingPaid {
		return false, nil // idempotent no-op
	}

	now := time.Now().UTC()
	if _, err := u.store.Append(ctx, tx, spec.BookingID, version, eventstore.NewEvent{
		Type: domevent.TypeBookingPaid, SchemaVersion: 1,
		Payload: domevent.BookingPaid{BookingID: spec.BookingID, AmountMinor: amount, Method: spec.Method}, Metadata: meta(spec.CorrelationID),
	}); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE booking.bookings SET status='PAID', paid_at=$2, settlement_time=$2, version=$3, updated_at=$2 WHERE id=$1::uuid`,
		spec.BookingID, now, version+1); err != nil {
		return false, err
	}

	byCat, err := heldByCategory(ctx, tx, spec.BookingID)
	if err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE booking.booking_items SET item_status='PAID' WHERE booking_id=$1::uuid AND item_status='HELD'`, spec.BookingID); err != nil {
		return false, err
	}
	// Issue one ticket per paid item (ON CONFLICT keeps it idempotent).
	rows, err := tx.Query(ctx, `SELECT id::text FROM booking.booking_items WHERE booking_id=$1::uuid AND item_status='PAID'`, spec.BookingID)
	if err != nil {
		return false, err
	}
	var itemIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return false, err
		}
		itemIDs = append(itemIDs, id)
	}
	rows.Close()
	for _, id := range itemIDs {
		code := spec.TicketCodeFn(id)
		if _, err := tx.Exec(ctx, `
			INSERT INTO booking.tickets (booking_id, booking_item_id, public_code)
			VALUES ($1::uuid, $2::uuid, $3) ON CONFLICT (booking_item_id) DO NOTHING`, spec.BookingID, id, code); err != nil {
			return false, err
		}
	}

	n := sum(byCat)
	for cat, cn := range byCat {
		if _, err := tx.Exec(ctx, `UPDATE booking.category_inventories SET held_quantity=held_quantity-$2, sold_quantity=sold_quantity+$2, updated_at=now() WHERE category_id=$1::uuid`, cat, cn); err != nil {
			return false, err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE booking.event_supply SET held_units=held_units-$2, sold_units=sold_units+$2, version=version+1, updated_at=now() WHERE event_id=$1::uuid`, eventID, n); err != nil {
		return false, err
	}
	// Publish sold_out if event supply is exhausted (SRS-F-10-01).
	var soldUnits, totalUnits int
	if err := tx.QueryRow(ctx, `SELECT sold_units, total_units FROM booking.event_supply WHERE event_id=$1::uuid`,
		eventID).Scan(&soldUnits, &totalUnits); err == nil && soldUnits >= totalUnits {
		soldOut := map[string]any{"eventId": eventID}
		_ = enqueueOutbox(ctx, tx, "booking.sold_out", spec.BookingID, soldOut)
	}
	if _, err := tx.Exec(ctx, `UPDATE booking.buyer_quotas SET held_quantity=held_quantity-$3, paid_quantity=paid_quantity+$3, updated_at=now() WHERE event_id=$1::uuid AND buyer_nik_hash=$2`, eventID, buyerHash, n); err != nil {
		return false, err
	}
	// Publish quota.exhausted if buyer reached max quota (OQ-13).
	var paidQty, maxQty int
	if err := tx.QueryRow(ctx, `SELECT paid_quantity, max_quantity FROM booking.buyer_quotas WHERE event_id=$1::uuid AND buyer_nik_hash=$2`,
		eventID, buyerHash).Scan(&paidQty, &maxQty); err == nil && paidQty >= maxQty {
		quotaExhausted := map[string]any{"eventId": eventID, "buyerNikHash": hex.EncodeToString(buyerHash)}
		_ = enqueueOutbox(ctx, tx, "booking.quota.exhausted", spec.BookingID, quotaExhausted)
	}

	paid := map[string]any{"bookingId": spec.BookingID, "publicRef": publicRef, "buyerEmail": email}
	ended := map[string]any{"bookingId": spec.BookingID, "outcome": "PAID"}
	if err := enqueueOutbox(ctx, tx, "booking.paid", spec.BookingID, paid); err != nil {
		return false, err
	}
	if err := enqueueOutbox(ctx, tx, "booking.journey.ended", spec.BookingID, ended); err != nil {
		return false, err
	}
	return true, nil
}

// Expire releases a booking's held stock (EXPIRED) or marks it REVIEW (payment found, stock held).
func (u *UnitOfWork) Expire(ctx context.Context, spec outbound.ExpireSpec) (bool, error) {
	var acted bool
	err := u.retry(ctx, func(tx pgx.Tx) error {
		a, err := u.expire(ctx, tx, spec)
		acted = a
		return err
	})
	return acted, err
}

func (u *UnitOfWork) expire(ctx context.Context, tx pgx.Tx, spec outbound.ExpireSpec) (bool, error) {
	var status vo.BookingStatus
	var version int
	var eventID, publicRef, email string
	var buyerHash []byte
	var amount int64
	err := tx.QueryRow(ctx, `SELECT status, version, event_id::text, public_ref, buyer_email, buyer_nik_hash, amount_minor
		FROM booking.bookings WHERE id=$1::uuid FOR UPDATE`, spec.BookingID).
		Scan(&status, &version, &eventID, &publicRef, &email, &buyerHash, &amount)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, inbound.ErrNotFound
	}
	if err != nil {
		return false, err
	}
	if !status.IsPayable() {
		return false, nil // already terminal
	}

	if spec.Review {
		if _, err := u.store.Append(ctx, tx, spec.BookingID, version, eventstore.NewEvent{
			Type: domevent.TypeBookingReview, SchemaVersion: 1,
			Payload: domevent.BookingReview{BookingID: spec.BookingID, AmountMinor: amount}, Metadata: meta(spec.CorrelationID),
		}); err != nil {
			return false, err
		}
		if _, err := tx.Exec(ctx, `UPDATE booking.bookings SET status='REVIEW', version=$2, updated_at=now() WHERE id=$1::uuid`, spec.BookingID, version+1); err != nil {
			return false, err
		}
		review := map[string]any{"bookingId": spec.BookingID, "buyerEmail": email, "amountMinor": amount}
		return true, enqueueOutbox(ctx, tx, "booking.review", spec.BookingID, review)
	}

	if _, err := u.store.Append(ctx, tx, spec.BookingID, version, eventstore.NewEvent{
		Type: domevent.TypeBookingExpired, SchemaVersion: 1,
		Payload: domevent.BookingExpired{BookingID: spec.BookingID, Reason: spec.Reason}, Metadata: meta(spec.CorrelationID),
	}); err != nil {
		return false, err
	}
	byCat, err := heldByCategory(ctx, tx, spec.BookingID)
	if err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE booking.booking_items SET item_status='RELEASED' WHERE booking_id=$1::uuid AND item_status='HELD'`, spec.BookingID); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE booking.bookings SET status='EXPIRED', ended_at=now(), version=$2, updated_at=now() WHERE id=$1::uuid`, spec.BookingID, version+1); err != nil {
		return false, err
	}
	n := sum(byCat)
	for cat, cn := range byCat {
		if _, err := tx.Exec(ctx, `UPDATE booking.category_inventories SET held_quantity=held_quantity-$2, updated_at=now() WHERE category_id=$1::uuid`, cat, cn); err != nil {
			return false, err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE booking.event_supply SET held_units=held_units-$2, version=version+1, updated_at=now() WHERE event_id=$1::uuid`, eventID, n); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE booking.buyer_quotas SET held_quantity=held_quantity-$3, updated_at=now() WHERE event_id=$1::uuid AND buyer_nik_hash=$2`, eventID, buyerHash, n); err != nil {
		return false, err
	}
	expired := map[string]any{"bookingId": spec.BookingID, "publicRef": publicRef, "buyerEmail": email, "reason": spec.Reason}
	ended := map[string]any{"bookingId": spec.BookingID, "outcome": "EXPIRED"}
	if err := enqueueOutbox(ctx, tx, "booking.expired", spec.BookingID, expired); err != nil {
		return false, err
	}
	return true, enqueueOutbox(ctx, tx, "booking.journey.ended", spec.BookingID, ended)
}

// ExtendDeadline extends payment_expires_at once per booking.
func (u *UnitOfWork) ExtendDeadline(ctx context.Context, spec outbound.DeadlineSpec) error {
	return u.retry(ctx, func(tx pgx.Tx) error {
		return u.extendDeadline(ctx, tx, spec)
	})
}

func (u *UnitOfWork) extendDeadline(ctx context.Context, tx pgx.Tx, spec outbound.DeadlineSpec) error {
	tag, err := tx.Exec(ctx, `
		UPDATE booking.bookings
		   SET payment_expires_at = payment_expires_at + ($2 || ' seconds')::interval,
		       deadline_extended_at = now(),
		       updated_at = now()
		 WHERE id = $1::uuid
		   AND deadline_extended_at IS NULL
		   AND status IN ('PENDING_PAYMENT', 'VERIFYING')`,
		spec.BookingID, spec.Seconds)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return inbound.ErrDeadlineAlreadyExtended
	}
	return nil
}

// PaymentLock transitions a payable booking to VERIFYING and stamps payment_started_at, locking the
// order against edits before a payment attempt (v4.4). It is idempotent: a booking already VERIFYING
// returns OK with no new event. Returns PAYMENT_EXPIRED when the window lapsed and BOOKING_NOT_PAYABLE
// when the booking is in any non-payable state. Retries on event-store concurrency (max 3).
func (u *UnitOfWork) PaymentLock(ctx context.Context, spec outbound.PaymentLockSpec) (outbound.PaymentLockResult, error) {
	var res outbound.PaymentLockResult
	err := u.retry(ctx, func(tx pgx.Tx) error {
		r, err := u.paymentLock(ctx, tx, spec)
		res = r
		return err
	})
	return res, err
}

func (u *UnitOfWork) paymentLock(ctx context.Context, tx pgx.Tx, spec outbound.PaymentLockSpec) (outbound.PaymentLockResult, error) {
	var status vo.BookingStatus
	var version int
	var amountMinor int64
	var paymentExpiresAt time.Time
	err := tx.QueryRow(ctx, `
		SELECT status, version, amount_minor, payment_expires_at
		  FROM booking.bookings WHERE id=$1::uuid FOR UPDATE`, spec.BookingID).
		Scan(&status, &version, &amountMinor, &paymentExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return outbound.PaymentLockResult{}, inbound.ErrNotFound
	}
	if err != nil {
		return outbound.PaymentLockResult{}, err
	}

	now := time.Now().UTC()
	// Payment window lapsed: not lockable regardless of status.
	if now.After(paymentExpiresAt) {
		return outbound.PaymentLockResult{Outcome: "PAYMENT_EXPIRED", AmountMinor: amountMinor, PaymentExpiresAt: paymentExpiresAt}, nil
	}
	if !status.IsPayable() {
		return outbound.PaymentLockResult{Outcome: "BOOKING_NOT_PAYABLE", AmountMinor: amountMinor, PaymentExpiresAt: paymentExpiresAt}, nil
	}

	// Already locked (VERIFYING): idempotent no-op, no new event.
	if status == vo.BookingVerifying {
		return outbound.PaymentLockResult{Outcome: "OK", Version: version, AmountMinor: amountMinor, PaymentExpiresAt: paymentExpiresAt}, nil
	}

	// First lock: PENDING_PAYMENT → VERIFYING. Append the aggregate event, then project the read model
	// and stamp payment_started_at in the same transaction (event-sourcing invariant).
	if _, err := u.store.Append(ctx, tx, spec.BookingID, version, eventstore.NewEvent{
		Type: domevent.TypeBookingLocked, SchemaVersion: 1,
		Payload:  domevent.BookingLocked{BookingID: spec.BookingID, Reason: "PAYMENT_STARTED"},
		Metadata: meta(spec.CorrelationID),
	}); err != nil {
		return outbound.PaymentLockResult{}, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE booking.bookings
		   SET status = 'VERIFYING',
		       payment_started_at = COALESCE(payment_started_at, $2),
		       version = $3,
		       updated_at = $2
		 WHERE id = $1::uuid`, spec.BookingID, now, version+1); err != nil {
		return outbound.PaymentLockResult{}, err
	}
	return outbound.PaymentLockResult{
		Outcome:          "OK",
		Version:          version + 1,
		AmountMinor:      amountMinor,
		PaymentExpiresAt: paymentExpiresAt,
	}, nil
}

// retry runs fn in a transaction, retrying on event-store concurrency up to maxRetries.
func (u *UnitOfWork) retry(ctx context.Context, fn func(pgx.Tx) error) error {
	var err error
	for attempt := 0; attempt < maxRetries; attempt++ {
		err = pkgpg.WithTx(ctx, u.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, fn)
		if err == nil {
			return nil
		}
		if errors.Is(err, eventstore.ErrConcurrency) {
			continue
		}
		return err
	}
	return err
}

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

func enqueueOutbox(ctx context.Context, tx pgx.Tx, routingKey, aggregateID string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO booking.outbox_events (routing_key, aggregate_id, schema_version, payload)
		VALUES ($1, $2::uuid, 1, $3)`, routingKey, aggregateID, raw)
	return err
}

func itemSnapshots(lines []outbound.HoldLine) []domevent.ItemSnapshot {
	out := make([]domevent.ItemSnapshot, 0, len(lines))
	for _, l := range lines {
		out = append(out, domevent.ItemSnapshot{CategoryID: l.CategoryID, UnitPriceMinor: l.UnitPriceMinor, AttendeeNIKMasked: l.AttendeeNIKMasked})
	}
	return out
}

func meta(correlationID string) map[string]string {
	return map[string]string{"correlationId": correlationID, "actor": "booking-service"}
}

func mapHoldErr(err error) error {
	if err == nil {
		return nil
	}
	if pkgpg.IsCode(err, pkgpg.CodeCheckViolation) {
		if pkgpg.ConstraintName(err) == "buyer_quotas_check" {
			return inbound.ErrBuyerQuota
		}
		return inbound.ErrCategoryUnavailable
	}
	if pkgpg.IsCode(err, pkgpg.CodeUniqueViolation) {
		switch pkgpg.ConstraintName(err) {
		case "uq_attendee_nik_per_event":
			return inbound.ErrNikRegistered
		case "uq_booking_item_nik_active":
			return inbound.ErrNikDuplicate
		}
	}
	return err
}

func mapEditErr(err error) error {
	if pkgpg.IsCode(err, pkgpg.CodeCheckViolation) {
		return inbound.ErrEditLimit
	}
	return err
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
