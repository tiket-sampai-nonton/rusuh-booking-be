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

	// Step 1: lock the admission first (serialises every attempt on one session) and read its own counters.
	var admStatus string
	var admExpiresAt time.Time
	var cfgGrace, outstanding, refusals int
	var lastRefusal *time.Time
	if err := tx.QueryRow(ctx, `
		SELECT status, expires_at, cfg_submit_grace_seconds, outstanding_units, buyer_nik_refusals, buyer_nik_last_refusal_at
		FROM booking.admissions WHERE id=$1::uuid AND visitor_id=$2::uuid FOR UPDATE`,
		spec.AdmissionID, spec.VisitorID).Scan(&admStatus, &admExpiresAt, &cfgGrace, &outstanding, &refusals, &lastRefusal); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return outbound.HoldResult{}, inbound.ErrSessionExpired
		}
		return outbound.HoldResult{}, err
	}
	if admStatus != "ACTIVE" && admStatus != "EDITABLE" {
		return outbound.HoldResult{}, inbound.ErrSessionExpired
	}
	grace := time.Duration(cfgGrace) * time.Second
	if time.Now().UTC().After(admExpiresAt.Add(grace)) {
		return outbound.HoldResult{}, inbound.ErrSessionExpired
	}
	// inGrace: request arrived after TTL but within the grace window.
	inGrace := time.Now().UTC().After(admExpiresAt)
	// The hold may take only this admission's own reserved units (F1): never another admission's.
	if n < 1 || n > outstanding {
		return outbound.HoldResult{}, inbound.ErrQuantityInvalid
	}

	// Step 2: SUPPLY lock (fixed order admission → supply → quota → category).
	if _, err := tx.Exec(ctx, `SELECT 1 FROM booking.event_supply WHERE event_id=$1::uuid FOR UPDATE`, spec.EventID); err != nil {
		return outbound.HoldResult{}, err
	}
	// The usecase read the event before this transaction; a cancel in between must still stop the hold (P2-9).
	var evStatus string
	if err := tx.QueryRow(ctx, `SELECT status FROM booking.events WHERE id=$1::uuid`, spec.EventID).Scan(&evStatus); err != nil {
		return outbound.HoldResult{}, err
	}
	if evStatus == "CANCELLED" {
		return outbound.HoldResult{}, inbound.ErrEventCancelled
	}

	// Step 3: QUOTA row — upsert (cap from the event) then lock; read held/paid/max.
	if _, err := tx.Exec(ctx, `
		INSERT INTO booking.buyer_quotas (event_id, buyer_nik_hash, max_quantity)
		SELECT $1::uuid, $2, ev.max_tickets_per_buyer_nik FROM booking.events ev WHERE ev.id=$1::uuid
		ON CONFLICT (event_id, buyer_nik_hash) DO NOTHING`, spec.EventID, spec.BuyerNIKHash); err != nil {
		return outbound.HoldResult{}, err
	}
	var heldQ, paidQ, maxQ int
	if err := tx.QueryRow(ctx, `SELECT held_quantity, paid_quantity, max_quantity FROM booking.buyer_quotas
		WHERE event_id=$1::uuid AND buyer_nik_hash=$2 FOR UPDATE`, spec.EventID, spec.BuyerNIKHash).Scan(&heldQ, &paidQ, &maxQ); err != nil {
		return outbound.HoldResult{}, err
	}

	// Step 4: CATEGORY rows in id order; live availability per requested category.
	byCat := map[string]int{}
	for _, l := range spec.Lines {
		byCat[l.CategoryID]++
	}
	cats := make([]string, 0, len(byCat))
	for c := range byCat {
		cats = append(cats, c)
	}
	rows, err := tx.Query(ctx, `
		SELECT category_id::text, capacity - held_quantity - sold_quantity
		  FROM booking.category_inventories WHERE category_id = ANY($1::uuid[]) ORDER BY category_id FOR UPDATE`, cats)
	if err != nil {
		return outbound.HoldResult{}, err
	}
	avail := map[string]int{}
	for rows.Next() {
		var id string
		var a int
		if err := rows.Scan(&id, &a); err != nil {
			rows.Close()
			return outbound.HoldResult{}, err
		}
		avail[id] = a
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return outbound.HoldResult{}, err
	}
	var refused []string
	for c, want := range byCat {
		if avail[c] < want {
			refused = append(refused, c)
		}
	}
	if len(refused) > 0 {
		// Typed error → rollback: the admission and its reserved units are untouched (the buyer may pick another category).
		return outbound.HoldResult{}, inbound.CategoryUnavailable(refused)
	}

	// Step 5: throttle after the refusal budget; capped exponential backoff on the injected clock.
	if spec.RefusalBudget > 0 && refusals >= spec.RefusalBudget && lastRefusal != nil {
		wait := spec.ThrottleBase << min(refusals-spec.RefusalBudget, 20)
		if wait <= 0 || wait > spec.ThrottleMax {
			wait = spec.ThrottleMax
		}
		if until := lastRefusal.Add(wait); spec.Now.Before(until) {
			secs := int(until.Sub(spec.Now).Seconds())
			if time.Duration(secs)*time.Second < until.Sub(spec.Now) {
				secs++
			}
			if secs < 1 {
				secs = 1
			}
			return outbound.HoldResult{}, inbound.BuyerNikThrottled(secs)
		}
	}

	// Step 6: buyer-NIK cap. The refusal counter must survive, so this is a result (commit), not an error.
	if heldQ+paidQ+n > maxQ {
		if _, err := tx.Exec(ctx, `
			UPDATE booking.admissions SET buyer_nik_refusals = buyer_nik_refusals + 1, buyer_nik_last_refusal_at = $2
			 WHERE id=$1::uuid`, spec.AdmissionID, spec.Now); err != nil {
			return outbound.HoldResult{}, err
		}
		return outbound.HoldResult{Refused: outbound.RefusedBuyerNikLimit}, nil
	}

	// Step 7: append event (optimistic) and project the read model (booking LAST).
	payload := domevent.BookingHeld{
		BookingID: spec.BookingID, EventID: spec.EventID, AdmissionID: spec.AdmissionID,
		AmountMinor: spec.AmountMinor, ItemQuantity: n, Items: itemSnapshots(spec.Lines),
	}
	if _, err := u.store.Append(ctx, tx, spec.BookingID, 0, eventstore.NewEvent{
		Type: domevent.TypeBookingHeld, SchemaVersion: 1, Payload: payload, Metadata: meta(spec.CorrelationID),
	}); err != nil {
		return outbound.HoldResult{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO booking.bookings (id, public_ref, event_id, admission_id, visitor_id, buyer_nik_hash,
			buyer_email_enc, status, amount_minor, item_quantity, payment_expires_at, version, account_id)
		VALUES ($1::uuid,$2,$3::uuid,$4::uuid,$5::uuid,$6,$7,'PENDING_PAYMENT',$8,$9,$10,1, NULLIF($11,'')::uuid)`,
		spec.BookingID, spec.PublicRef, spec.EventID, spec.AdmissionID, spec.VisitorID, spec.BuyerNIKHash,
		spec.BuyerEmailEnc, spec.AmountMinor, n, spec.PaymentExpiresAt, spec.AccountID); err != nil {
		return outbound.HoldResult{}, mapHoldErr(err)
	}
	itemIDs := make([]string, 0, len(spec.Lines))
	for _, l := range spec.Lines {
		var itemID string
		if err := tx.QueryRow(ctx, `
			INSERT INTO booking.booking_items (booking_id, event_id, category_id, unit_price_minor,
				attendee_nik_hash, attendee_nik_enc, attendee_nik_masked, attendee_name, item_status)
			VALUES ($1::uuid,$2::uuid,$3::uuid,$4,$5,$6,$7,$8,'HELD') RETURNING id`,
			spec.BookingID, spec.EventID, l.CategoryID, l.UnitPriceMinor,
			l.AttendeeNIKHash, l.AttendeeNIKEnc, l.AttendeeNIKMasked, l.AttendeeName).Scan(&itemID); err != nil {
			return outbound.HoldResult{}, mapHoldErr(err)
		}
		itemIDs = append(itemIDs, itemID) // loop iteration order == spec.Lines order
	}

	// Step 8: counters. allocated_units drops by this admission's own outstanding units only; the n held
	// units move to held_units and the rest (outstanding - n) become free stock in this commit (F1/F2).
	for cat, cn := range byCat {
		if _, err := tx.Exec(ctx, `UPDATE booking.category_inventories SET held_quantity=held_quantity+$2, updated_at=now() WHERE category_id=$1::uuid`, cat, cn); err != nil {
			return outbound.HoldResult{}, mapHoldErr(err)
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE booking.event_supply SET allocated_units=allocated_units-$2, held_units=held_units+$3, version=version+1, updated_at=now() WHERE event_id=$1::uuid`, spec.EventID, outstanding, n); err != nil {
		return outbound.HoldResult{}, mapHoldErr(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE booking.buyer_quotas SET held_quantity=held_quantity+$3, updated_at=now() WHERE event_id=$1::uuid AND buyer_nik_hash=$2`, spec.EventID, spec.BuyerNIKHash, n); err != nil {
		return outbound.HoldResult{}, mapHoldErr(err)
	}

	// Step 9: bind the buyer and settle the counter in the same statement as the status change.
	version := 1
	if !spec.CfgEditEnabled || inGrace {
		lockReason := "EDIT_DISABLED"
		if inGrace {
			lockReason = "SELECTION_EXPIRED"
		}
		if _, err := tx.Exec(ctx, `
			UPDATE booking.admissions
			   SET status='CONVERTED', lock_reason=$2, locked_at=now(), ended_at=now(), end_reason='CONVERTED',
			       outstanding_units=0, buyer_nik_hash=$3, buyer_nik_masked=NULLIF($4,''), buyer_email_enc=NULLIF($5,'')
			 WHERE id=$1::uuid`, spec.AdmissionID, lockReason, spec.BuyerNIKHash, spec.BuyerNIKMasked, spec.BuyerEmailEnc); err != nil {
			return outbound.HoldResult{}, err
		}
		if _, err := u.store.Append(ctx, tx, spec.BookingID, 1, eventstore.NewEvent{
			Type: domevent.TypeBookingLocked, SchemaVersion: 1,
			Payload:  domevent.BookingLocked{BookingID: spec.BookingID, Reason: lockReason},
			Metadata: meta(spec.CorrelationID),
		}); err != nil {
			return outbound.HoldResult{}, err
		}
		// The read model must follow the stream: BookingHeld is v1, BookingLocked v2.
		if _, err := tx.Exec(ctx, `UPDATE booking.bookings SET version=2, updated_at=now() WHERE id=$1::uuid`, spec.BookingID); err != nil {
			return outbound.HoldResult{}, err
		}
		version = 2
	} else if _, err := tx.Exec(ctx, `
		UPDATE booking.admissions
		   SET status='EDITABLE', outstanding_units=0, buyer_nik_hash=$2, buyer_nik_masked=NULLIF($3,''), buyer_email_enc=NULLIF($4,'')
		 WHERE id=$1::uuid`, spec.AdmissionID, spec.BuyerNIKHash, spec.BuyerNIKMasked, spec.BuyerEmailEnc); err != nil {
		return outbound.HoldResult{}, err
	}

	return outbound.HoldResult{
		BookingID: spec.BookingID, PublicRef: spec.PublicRef, Version: version,
		Status: string(vo.BookingPendingPayment), AmountMinor: spec.AmountMinor, PaymentExpiresAt: spec.PaymentExpiresAt,
		ItemIDs: itemIDs,
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
	// Design §9.3 Step 2: lock admission first — must be EDITABLE, within grace, under edit limit.
	var admStatus string
	var admExpiresAt time.Time
	var editCount, cfgMaxEdits, cfgGrace int
	if err := tx.QueryRow(ctx, `
		SELECT status, expires_at, edit_count, cfg_max_edits, cfg_submit_grace_seconds
		FROM booking.admissions WHERE id=$1::uuid FOR UPDATE`, spec.AdmissionID).
		Scan(&admStatus, &admExpiresAt, &editCount, &cfgMaxEdits, &cfgGrace); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, inbound.ErrNotFound
		}
		return 0, err
	}
	if admStatus == "CONVERTED" {
		return 0, inbound.ErrBookingLocked
	}
	if admStatus != "EDITABLE" {
		return 0, inbound.ErrSessionExpired
	}
	grace := time.Duration(cfgGrace) * time.Second
	if time.Now().UTC().After(admExpiresAt.Add(grace)) {
		return 0, inbound.ErrSessionExpired
	}
	if editCount >= cfgMaxEdits {
		return 0, inbound.ErrEditLimit
	}

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

	// Lock order aligned with hold (admission → booking → supply → quota → category): an increase needs free
	// stock from the supply row, so it is locked before the quota and category rows.
	if _, err := tx.Exec(ctx, `SELECT 1 FROM booking.event_supply WHERE event_id=$1::uuid FOR UPDATE`, spec.EventID); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `SELECT 1 FROM booking.buyer_quotas WHERE event_id=$1::uuid AND buyer_nik_hash=$2 FOR UPDATE`,
		spec.EventID, spec.BuyerNIKHash); err != nil {
		return 0, err
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
		// E1 (ADR-030, PROVISIONAL pending the human gate; E2 would refuse delta > 0): after hold the admission owns no reserved units, so an edit moves only held_units. An
		// increase is limited by free stock (the supply CHECK is the backstop → CATEGORY_UNAVAILABLE) and a
		// decrease returns units to free stock immediately.
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
	// Step 1: plain read — discover eventID and admissionID (no lock yet, §9.0).
	var eventID, admissionID string
	if err := tx.QueryRow(ctx, `
		SELECT event_id::text, admission_id::text FROM booking.bookings WHERE id=$1::uuid`,
		spec.BookingID).Scan(&eventID, &admissionID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, inbound.ErrNotFound
		}
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
	var buyerHash []byte
	var amount int64
	var publicRef, email string
	var paymentExpiresAt time.Time
	if err := tx.QueryRow(ctx, `
		SELECT status, version, buyer_nik_hash, amount_minor, public_ref, COALESCE(buyer_email_enc, ''), payment_expires_at
		  FROM booking.bookings WHERE id=$1::uuid FOR UPDATE`, spec.BookingID).
		Scan(&status, &version, &buyerHash, &amount, &publicRef, &email, &paymentExpiresAt); err != nil {
		return false, err
	}
	if status == vo.BookingPaid || status == vo.BookingReview {
		return false, nil // idempotent no-op (design §9.5: PAID/REVIEW → no-op, ack)
	}

	// Step 4: fetch queue_entry_id (admission already locked — plain read safe).
	var queueEntryID string
	if err := tx.QueryRow(ctx, `
		SELECT queue_entry_id::text FROM booking.admissions WHERE id=$1::uuid`, admissionID).
		Scan(&queueEntryID); err != nil {
		return false, err
	}

	now := time.Now().UTC()

	// Design §9.5: EXPIRED/CANCELLED → late payment arrived → REVIEW (no stock change needed).
	if status == vo.BookingExpired || status == vo.BookingCancelled {
		if _, err := u.store.Append(ctx, tx, spec.BookingID, version, eventstore.NewEvent{
			Type: domevent.TypeBookingReview, SchemaVersion: 1,
			Payload:  domevent.BookingReview{BookingID: spec.BookingID, AmountMinor: amount},
			Metadata: meta(spec.CorrelationID),
		}); err != nil {
			return false, err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE booking.bookings SET status='REVIEW', version=$2, updated_at=now()
			 WHERE id=$1::uuid`, spec.BookingID, version+1); err != nil {
			return false, err
		}
		review := map[string]any{"bookingId": spec.BookingID, "buyerEmailEnc": email, "amountMinor": amount}
		return false, enqueueOutbox(ctx, tx, "booking.review", spec.BookingID, review)
	}

	// Step 5: lock event_supply (§9.0: after booking, before quota/categories).
	if _, err := tx.Exec(ctx, `
		SELECT 1 FROM booking.event_supply WHERE event_id=$1::uuid FOR UPDATE`, eventID); err != nil {
		return false, err
	}

	// Design §9.5, Property 7: a settlement time known to be after payment_expires_at is late → REVIEW (release held
	// stock). An unknown time (zero: the consumer could not parse it) is timely (decision D1): the booking is still
	// payable, so its stock is still held, and the charge expires at the same deadline.
	// P2-8: a charge whose amount differs from the booking's amount is not the sale we hold stock for, so it takes the
	// same REVIEW path. A zero spec.AmountMinor means the amount is unknown and is not checked.
	late := !spec.SettlementTime.IsZero() && spec.SettlementTime.After(paymentExpiresAt)
	mismatch := spec.AmountMinor > 0 && spec.AmountMinor != amount
	if late || mismatch {
		// Release held stock and mark REVIEW — no tickets issued.
		if _, err := u.store.Append(ctx, tx, spec.BookingID, version, eventstore.NewEvent{
			Type: domevent.TypeBookingReview, SchemaVersion: 1,
			Payload:  domevent.BookingReview{BookingID: spec.BookingID, AmountMinor: amount},
			Metadata: meta(spec.CorrelationID),
		}); err != nil {
			return false, err
		}
		if err := releaseHeldItems(ctx, tx, spec.BookingID, eventID, buyerHash); err != nil {
			return false, err
		}
		// settlement_time stays NULL when the gateway gave no time (mismatch without a settlement time).
		var settlementTime *time.Time
		if !spec.SettlementTime.IsZero() {
			settlementTime = &spec.SettlementTime
		}
		if _, err := tx.Exec(ctx, `
			UPDATE booking.bookings SET status='REVIEW', settlement_time=$2, version=$3, updated_at=now()
			 WHERE id=$1::uuid`, spec.BookingID, settlementTime, version+1); err != nil {
			return false, err
		}
		review := map[string]any{"bookingId": spec.BookingID, "buyerEmailEnc": email, "amountMinor": amount}
		ended := map[string]any{"eventId": eventID, "queueEntryId": queueEntryID, "outcome": "EXPIRED"}
		if err := enqueueOutbox(ctx, tx, "booking.review", spec.BookingID, review); err != nil {
			return false, err
		}
		return false, enqueueOutbox(ctx, tx, "booking.journey.ended", spec.BookingID, ended)
	}

	// Happy path: timely payment → PAID + tickets.
	if _, err := u.store.Append(ctx, tx, spec.BookingID, version, eventstore.NewEvent{
		Type: domevent.TypeBookingPaid, SchemaVersion: 1,
		Payload:  domevent.BookingPaid{BookingID: spec.BookingID, AmountMinor: amount, Method: spec.Method},
		Metadata: meta(spec.CorrelationID),
	}); err != nil {
		return false, err
	}
	settledAt := spec.SettlementTime
	if settledAt.IsZero() {
		settledAt = now // unknown settlement time: the column records when Booking settled
	}
	if _, err := tx.Exec(ctx, `
		UPDATE booking.bookings SET status='PAID', paid_at=$2, settlement_time=$3, version=$4, updated_at=$2
		 WHERE id=$1::uuid`, spec.BookingID, now, settledAt, version+1); err != nil {
		return false, err
	}

	byCat, err := heldByCategory(ctx, tx, spec.BookingID)
	if err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE booking.booking_items SET item_status='PAID'
		 WHERE booking_id=$1::uuid AND item_status='HELD'`, spec.BookingID); err != nil {
		return false, err
	}
	// Issue one ticket per paid item (ON CONFLICT keeps it idempotent).
	rows, err := tx.Query(ctx, `
		SELECT id::text FROM booking.booking_items WHERE booking_id=$1::uuid AND item_status='PAID'`, spec.BookingID)
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
		if _, err := tx.Exec(ctx, `
			UPDATE booking.category_inventories SET held_quantity=held_quantity-$2, sold_quantity=sold_quantity+$2, updated_at=now()
			 WHERE category_id=$1::uuid`, cat, cn); err != nil {
			return false, err
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE booking.event_supply SET held_units=held_units-$2, sold_units=sold_units+$2, version=version+1, updated_at=now()
		 WHERE event_id=$1::uuid`, eventID, n); err != nil {
		return false, err
	}
	// Publish sold_out only when nothing can come back (SRS-F-09-05): free + allocated + held = 0 ⟺ sold = total
	// with no allocation and no hold outstanding. Other buyers' reservations may still be released.
	var allocUnits, heldUnits2, soldUnits2, totalCap int
	if err := tx.QueryRow(ctx, `
		SELECT allocated_units, held_units, sold_units, total_capacity FROM booking.event_supply WHERE event_id=$1::uuid`,
		eventID).Scan(&allocUnits, &heldUnits2, &soldUnits2, &totalCap); err == nil &&
		allocUnits+heldUnits2 == 0 && soldUnits2 >= totalCap {
		soldOut := map[string]any{"eventId": eventID}
		_ = enqueueOutbox(ctx, tx, "booking.sold_out", spec.BookingID, soldOut)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE booking.buyer_quotas SET held_quantity=held_quantity-$3, paid_quantity=paid_quantity+$3, updated_at=now()
		 WHERE event_id=$1::uuid AND buyer_nik_hash=$2`, eventID, buyerHash, n); err != nil {
		return false, err
	}
	// Publish quota.exhausted if buyer reached max quota (OQ-13).
	var paidQty, maxQty int
	if err := tx.QueryRow(ctx, `
		SELECT paid_quantity, max_quantity FROM booking.buyer_quotas WHERE event_id=$1::uuid AND buyer_nik_hash=$2`,
		eventID, buyerHash).Scan(&paidQty, &maxQty); err == nil && paidQty >= maxQty {
		quotaExhausted := map[string]any{"eventId": eventID, "buyerNikHash": hex.EncodeToString(buyerHash)}
		_ = enqueueOutbox(ctx, tx, "booking.quota.exhausted", spec.BookingID, quotaExhausted)
	}

	// §9.5: convert admission EDITABLE→CONVERTED(BOOKING_ENDED) if still open.
	// (paymentLock §9.4 normally converts it first; this handles the race where payment.succeeded
	// arrives before payment-lock was called.)
	if admStatus == "EDITABLE" {
		if err := releaseOutstanding(ctx, tx, eventID, admissionID); err != nil {
			return false, err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE booking.admissions
			   SET status='CONVERTED', outstanding_units=0, lock_reason='BOOKING_ENDED',
			       locked_at=now(), ended_at=now(), end_reason='CONVERTED'
			 WHERE id=$1::uuid AND status='EDITABLE'`, admissionID); err != nil {
			return false, err
		}
	}

	// Build full booking.paid payload per schema (docs/contracts/events/booking.paid.v1.schema.json).
	type ticketLine struct {
		TicketCode        string `json:"ticketCode"`
		AttendeeName      string `json:"attendeeName"`
		AttendeeNIKMasked string `json:"attendeeNikMasked"`
		CategoryCode      string `json:"categoryCode"`
	}
	var ticketLines []ticketLine
	ticketRows, terr := tx.Query(ctx, `
		SELECT t.public_code, bi.attendee_name, bi.attendee_nik_masked, tc.code
		  FROM booking.tickets t
		  JOIN booking.booking_items bi ON bi.id = t.booking_item_id
		  JOIN booking.ticket_categories tc ON tc.id = bi.category_id
		 WHERE t.booking_id = $1::uuid`, spec.BookingID)
	if terr == nil {
		defer ticketRows.Close()
		for ticketRows.Next() {
			var tl ticketLine
			_ = ticketRows.Scan(&tl.TicketCode, &tl.AttendeeName, &tl.AttendeeNIKMasked, &tl.CategoryCode)
			ticketLines = append(ticketLines, tl)
		}
		ticketRows.Close()
	}
	ticketLinkToken := ""
	if spec.TicketLinkTokenFn != nil {
		ticketLinkToken = spec.TicketLinkTokenFn(spec.BookingID)
	}

	var evName, evVenue string
	var evStartsAt time.Time
	if err := tx.QueryRow(ctx, `SELECT name, venue, starts_at FROM booking.events WHERE id=$1::uuid`, eventID).
		Scan(&evName, &evVenue, &evStartsAt); err != nil {
		return false, err
	}
	paid := map[string]any{
		"bookingId":       spec.BookingID,
		"publicRef":       publicRef,
		"buyerEmailEnc":   email,
		"event":           map[string]any{"eventId": eventID, "name": evName, "venue": evVenue, "startsAt": evStartsAt.UTC().Format(time.RFC3339)},
		"tickets":         ticketLines,
		"ticketLinkToken": ticketLinkToken,
	}
	ended := map[string]any{"eventId": eventID, "queueEntryId": queueEntryID, "outcome": "PAID"}
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
	// Step 1: plain read — discover eventID and admissionID (no lock yet, §9.0).
	var eventID, admissionID string
	if err := tx.QueryRow(ctx, `
		SELECT event_id::text, admission_id::text FROM booking.bookings WHERE id=$1::uuid`,
		spec.BookingID).Scan(&eventID, &admissionID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, inbound.ErrNotFound
		}
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

	// Step 4: lock event_supply (§9.0: after booking, before quota/categories).
	if _, err := tx.Exec(ctx, `
		SELECT 1 FROM booking.event_supply WHERE event_id=$1::uuid FOR UPDATE`, eventID); err != nil {
		return false, err
	}

	// Step 5: fetch queue_entry_id for journey.ended payload (admission already locked — plain read safe).
	var queueEntryID string
	if err := tx.QueryRow(ctx, `
		SELECT queue_entry_id::text FROM booking.admissions WHERE id=$1::uuid`, admissionID).
		Scan(&queueEntryID); err != nil {
		return false, err
	}

	if spec.Review {
		// REVIEW path: late payment / cancel — mark REVIEW and give the held stock back (P0-5).
		if _, err := u.store.Append(ctx, tx, spec.BookingID, version, eventstore.NewEvent{
			Type: domevent.TypeBookingReview, SchemaVersion: 1,
			Payload:  domevent.BookingReview{BookingID: spec.BookingID, AmountMinor: amount},
			Metadata: meta(spec.CorrelationID),
		}); err != nil {
			return false, err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE booking.bookings SET status='REVIEW', version=$2, updated_at=now()
			 WHERE id=$1::uuid`, spec.BookingID, version+1); err != nil {
			return false, err
		}
		if err := releaseHeldItems(ctx, tx, spec.BookingID, eventID, buyerHash); err != nil {
			return false, err
		}
		// §7.2 / §9.6: convert EDITABLE admission on any terminal booking path.
		if admStatus == "EDITABLE" {
			if err := releaseOutstanding(ctx, tx, eventID, admissionID); err != nil {
				return false, err
			}
			if _, err := tx.Exec(ctx, `
				UPDATE booking.admissions
				   SET status='CONVERTED', outstanding_units=0, lock_reason='BOOKING_ENDED',
				       locked_at=now(), ended_at=now(), end_reason='CONVERTED'
				 WHERE id=$1::uuid AND status='EDITABLE'`, admissionID); err != nil {
				return false, err
			}
		}
		review := map[string]any{"bookingId": spec.BookingID, "buyerEmailEnc": email, "amountMinor": amount}
		if err := enqueueOutbox(ctx, tx, "booking.review", spec.BookingID, review); err != nil {
			return false, err
		}
		ended := map[string]any{"eventId": eventID, "queueEntryId": queueEntryID, "outcome": "EXPIRED"}
		return true, enqueueOutbox(ctx, tx, "booking.journey.ended", spec.BookingID, ended)
	}

	// EXPIRED path (NOT_PAID / NO_ATTEMPT / grace exhausted).
	if _, err := u.store.Append(ctx, tx, spec.BookingID, version, eventstore.NewEvent{
		Type: domevent.TypeBookingExpired, SchemaVersion: 1,
		Payload:  domevent.BookingExpired{BookingID: spec.BookingID, Reason: spec.Reason},
		Metadata: meta(spec.CorrelationID),
	}); err != nil {
		return false, err
	}
	if err := releaseHeldItems(ctx, tx, spec.BookingID, eventID, buyerHash); err != nil {
		return false, err
	}
	// Use CANCELLED status when reason is CANCELLED, otherwise EXPIRED.
	finalStatus := "EXPIRED"
	if spec.Reason == "CANCELLED" {
		finalStatus = "CANCELLED"
	}
	if _, err := tx.Exec(ctx, `
		UPDATE booking.bookings SET status=$3, ended_at=now(), version=$2, updated_at=now()
		 WHERE id=$1::uuid`, spec.BookingID, version+1, finalStatus); err != nil {
		return false, err
	}
	// §7.2 NOT_PAID/NO_ATTEMPT: convert admission EDITABLE→CONVERTED(BOOKING_ENDED).
	if admStatus == "EDITABLE" {
		if err := releaseOutstanding(ctx, tx, eventID, admissionID); err != nil {
			return false, err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE booking.admissions
			   SET status='CONVERTED', outstanding_units=0, lock_reason='BOOKING_ENDED',
			       locked_at=now(), ended_at=now(), end_reason='CONVERTED'
			 WHERE id=$1::uuid AND status='EDITABLE'`, admissionID); err != nil {
			return false, err
		}
	}
	expired := map[string]any{"bookingId": spec.BookingID, "publicRef": publicRef, "buyerEmailEnc": email, "reason": spec.Reason}
	ended := map[string]any{"eventId": eventID, "queueEntryId": queueEntryID, "outcome": "EXPIRED"}
	if err := enqueueOutbox(ctx, tx, "booking.expired", spec.BookingID, expired); err != nil {
		return false, err
	}
	return true, enqueueOutbox(ctx, tx, "booking.journey.ended", spec.BookingID, ended)
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
	_, err := u.store.Append(ctx, tx, spec.BookingID, version, eventstore.NewEvent{
		Type: domevent.TypeBookingDeadlineExtended, SchemaVersion: 1,
		Payload:  domevent.BookingDeadlineExtended{BookingID: spec.BookingID, Seconds: spec.Seconds, Reason: spec.Reason},
		Metadata: meta(spec.BookingID),
	})
	return err
}

// PaymentLock stamps payment_started_at, locking the order against edits before a payment attempt (v4.4).
// It is idempotent: a booking already stamped returns OK with no new event. A VERIFYING booking without a
// stamp is stamped like a PENDING_PAYMENT one (P1-5). Returns PAYMENT_EXPIRED when the window lapsed and
// BOOKING_NOT_PAYABLE when the booking is in any non-payable state. Retries on event-store concurrency (max 3).
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
	// Step 1: plain read — discover eventID, admissionID, item_quantity (no lock yet, §9.0).
	var eventID, admissionID string
	var itemQuantity int
	if err := tx.QueryRow(ctx, `
		SELECT event_id::text, admission_id::text, item_quantity
		  FROM booking.bookings WHERE id=$1::uuid`, spec.BookingID).
		Scan(&eventID, &admissionID, &itemQuantity); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return outbound.PaymentLockResult{}, inbound.ErrNotFound
		}
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

	now := time.Now().UTC()
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
	if _, err := u.store.Append(ctx, tx, spec.BookingID, version, eventstore.NewEvent{
		Type:          domevent.TypeBookingLocked,
		SchemaVersion: 1,
		Payload:       domevent.BookingLocked{BookingID: spec.BookingID, Reason: "PAYMENT_STARTED"},
		Metadata:      meta(spec.CorrelationID),
	}); err != nil {
		return outbound.PaymentLockResult{}, err
	}

	return outbound.PaymentLockResult{
		Outcome:          "OK",
		Version:          version + 1,
		AmountMinor:      amountMinor,
		PaymentExpiresAt: paymentExpiresAt,
	}, nil
}

// StartVerifying is Phase A of the expiry sweeper: PENDING_PAYMENT past deadline → VERIFYING (design §7.2).
func (u *UnitOfWork) StartVerifying(ctx context.Context, bookingID string, verifyGraceSeconds int) (bool, error) {
	var acted bool
	err := u.retry(ctx, func(tx pgx.Tx) error {
		var status vo.BookingStatus
		var version int
		var paymentExpiresAt time.Time
		err := tx.QueryRow(ctx, `SELECT status, version, payment_expires_at FROM booking.bookings WHERE id=$1::uuid FOR UPDATE`, bookingID).
			Scan(&status, &version, &paymentExpiresAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return inbound.ErrNotFound
		}
		if err != nil {
			return err
		}
		if status != vo.BookingPendingPayment {
			return nil // already VERIFYING or terminal — idempotent no-op
		}
		if time.Now().UTC().Before(paymentExpiresAt) {
			return nil // not yet past deadline
		}
		grace := time.Duration(verifyGraceSeconds) * time.Second
		verificationUntil := time.Now().UTC().Add(grace)
		if _, err := u.store.Append(ctx, tx, bookingID, version, eventstore.NewEvent{
			Type: domevent.TypePaymentVerifying, SchemaVersion: 1,
			Payload:  domevent.PaymentVerifying{BookingID: bookingID},
			Metadata: meta(bookingID),
		}); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE booking.bookings SET status='VERIFYING', version=$2, verification_until=$3, updated_at=now() WHERE id=$1::uuid`,
			bookingID, version+1, verificationUntil); err != nil {
			return err
		}
		acted = true
		return nil
	})
	return acted, err
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

// releaseHeldItems gives back the stock a booking still holds: its HELD items become RELEASED, and the category,
// event-supply and buyer-quota held counters drop by the same amount. The caller already holds the supply lock and
// the quota row. Does nothing when the booking has no HELD items, so a repeated call cannot release twice.
func releaseHeldItems(ctx context.Context, tx pgx.Tx, bookingID, eventID string, buyerHash []byte) error {
	byCat, err := heldByCategory(ctx, tx, bookingID)
	if err != nil {
		return err
	}
	n := sum(byCat)
	if n == 0 {
		return nil
	}
	if _, err := tx.Exec(ctx, `
		UPDATE booking.booking_items SET item_status='RELEASED'
		 WHERE booking_id=$1::uuid AND item_status='HELD'`, bookingID); err != nil {
		return err
	}
	for cat, cn := range byCat {
		if _, err := tx.Exec(ctx, `
			UPDATE booking.category_inventories SET held_quantity=held_quantity-$2, updated_at=now()
			 WHERE category_id=$1::uuid`, cat, cn); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE booking.event_supply SET held_units=held_units-$2, version=version+1, updated_at=now()
		 WHERE event_id=$1::uuid`, eventID, n); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		UPDATE booking.buyer_quotas SET held_quantity=held_quantity-$3, updated_at=now()
		 WHERE event_id=$1::uuid AND buyer_nik_hash=$2`, eventID, buyerHash, n)
	return err
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
			return inbound.ErrBuyerNikLimitReached
		}
		return inbound.ErrCategoryUnavailable
	}
	if pkgpg.IsCode(err, pkgpg.CodeUniqueViolation) {
		switch pkgpg.ConstraintName(err) {
		case "uq_attendee_nik_per_event":
			return inbound.ErrNikRegistered
		case "uq_booking_item_nik_active":
			return inbound.ErrNikDuplicate
		case "bookings_admission_id_key":
			// Satu admission (sesi) hanya boleh menjadi satu booking; hold kedua dengan key baru bukan 500.
			return inbound.ErrAdmissionToken
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
