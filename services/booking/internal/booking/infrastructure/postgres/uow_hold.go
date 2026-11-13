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

// Hold performs the atomic hold (invariant #1). Retries on event-store concurrency (max 3).
func (u *UnitOfWork) Hold(ctx context.Context, spec outbound.HoldSpec) (outbound.HoldResult, error) {
	return inTx(ctx, u, func(tx pgx.Tx) (outbound.HoldResult, error) { return u.hold(ctx, tx, spec) })
}

func (u *UnitOfWork) hold(ctx context.Context, tx pgx.Tx, spec outbound.HoldSpec) (outbound.HoldResult, error) {
	n := len(spec.Lines)
	now := clockOr(spec.Now)

	// Step 1: lock the admission first (serialises every attempt on one session) and read its own counters.
	adm, err := lockHoldAdmission(ctx, tx, spec)
	if err != nil {
		return outbound.HoldResult{}, err
	}
	if adm.status != "ACTIVE" && adm.status != "EDITABLE" {
		return outbound.HoldResult{}, inbound.ErrSessionExpired
	}
	if now.After(adm.expiresAt.Add(adm.grace)) {
		return outbound.HoldResult{}, inbound.ErrSessionExpired
	}
	// inGrace: request arrived after TTL but within the grace window.
	inGrace := now.After(adm.expiresAt)
	// The hold may take only this admission's own reserved units (F1): never another admission's.
	if n < 1 || n > adm.outstanding {
		return outbound.HoldResult{}, inbound.ErrQuantityInvalid
	}
	// A cancel committed before the admission lock stops the hold here; the same check repeats after the supply lock.
	if adm.eventStatus == "CANCELLED" {
		return outbound.HoldResult{}, inbound.ErrEventCancelled
	}

	// Step 2: SUPPLY lock (fixed order admission → supply → quota → category).
	if err := lockEventSupply(ctx, tx, spec.EventID); err != nil {
		return outbound.HoldResult{}, err
	}

	// Step 3: QUOTA row, upserted and locked, with the event status read again (P2-9, B10).
	q, err := lockBuyerQuota(ctx, tx, spec.EventID, spec.BuyerNIKHash)
	if err != nil {
		return outbound.HoldResult{}, err
	}
	if q.eventStatus == "CANCELLED" {
		return outbound.HoldResult{}, inbound.ErrEventCancelled
	}

	// Step 4: CATEGORY rows in id order; live availability per requested category.
	byCat := countByCategory(spec.Lines)
	if err := checkCategoryStock(ctx, tx, byCat); err != nil {
		return outbound.HoldResult{}, err
	}

	// Step 5: throttle after the refusal budget.
	if secs := throttleSeconds(spec, adm.refusals, adm.lastRefusal); secs > 0 {
		return outbound.HoldResult{}, inbound.BuyerNikThrottled(secs)
	}

	// Step 6: buyer-NIK cap. The refusal counter must survive, so this is a result (commit), not an error.
	if q.held+q.paid+n > q.max {
		if _, err := tx.Exec(ctx, `
			UPDATE booking.admissions SET buyer_nik_refusals = buyer_nik_refusals + 1, buyer_nik_last_refusal_at = $2
			 WHERE id=$1::uuid`, spec.AdmissionID, spec.Now); err != nil {
			return outbound.HoldResult{}, err
		}
		return outbound.HoldResult{Refused: outbound.RefusedBuyerNikLimit}, nil
	}

	// Step 7: the events come first, so the bookings row is written with its final version (no follow-up UPDATE).
	locked := !spec.CfgEditEnabled || inGrace
	lockReason := "EDIT_DISABLED"
	if inGrace {
		lockReason = "SELECTION_EXPIRED"
	}
	version, err := u.appendHoldEvents(ctx, tx, spec, locked, lockReason)
	if err != nil {
		return outbound.HoldResult{}, err
	}
	itemIDs, err := insertHeldBooking(ctx, tx, spec, version)
	if err != nil {
		return outbound.HoldResult{}, err
	}

	// Step 8: counters in one statement.
	if err := addHeldCounters(ctx, tx, spec, byCat, adm.outstanding); err != nil {
		return outbound.HoldResult{}, err
	}

	// Step 9: bind the buyer and settle the counter in the same statement as the status change.
	if err := bindHoldAdmission(ctx, tx, spec, locked, lockReason); err != nil {
		return outbound.HoldResult{}, err
	}

	return outbound.HoldResult{
		BookingID: spec.BookingID, PublicRef: spec.PublicRef, Version: version,
		Status: string(vo.BookingPendingPayment), AmountMinor: spec.AmountMinor, PaymentExpiresAt: spec.PaymentExpiresAt,
		ItemIDs: itemIDs,
	}, nil
}

// holdAdmission is the admission state the hold checks, read under the admission lock.
type holdAdmission struct {
	status, eventStatus   string
	expiresAt             time.Time
	grace                 time.Duration
	outstanding, refusals int
	lastRefusal           *time.Time
}

// lockHoldAdmission locks the visitor's admission and reads its counters. The event status comes in the same
// statement, so the join adds no round trip. An unknown admission (or another visitor's) is ErrSessionExpired.
func lockHoldAdmission(ctx context.Context, tx pgx.Tx, spec outbound.HoldSpec) (holdAdmission, error) {
	var a holdAdmission
	var cfgGrace int
	if err := tx.QueryRow(ctx, `
		SELECT a.status, a.expires_at, a.cfg_submit_grace_seconds, a.outstanding_units,
		       a.buyer_nik_refusals, a.buyer_nik_last_refusal_at, e.status
		  FROM booking.admissions a JOIN booking.events e ON e.id = a.event_id
		 WHERE a.id=$1::uuid AND a.visitor_id=$2::uuid FOR UPDATE OF a`,
		spec.AdmissionID, spec.VisitorID).Scan(&a.status, &a.expiresAt, &cfgGrace, &a.outstanding, &a.refusals, &a.lastRefusal, &a.eventStatus); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return holdAdmission{}, inbound.ErrSessionExpired
		}
		return holdAdmission{}, err
	}
	a.grace = time.Duration(cfgGrace) * time.Second
	return a, nil
}

// buyerQuota is the buyer's quota row for the event, plus the event status read in the same statement.
type buyerQuota struct {
	held, paid, max int
	eventStatus     string
}

// lockBuyerQuota upserts and locks the buyer's quota row and reads the event status again in the same statement:
// a cancel that committed after the usecase read the event still stops the hold, before any stock is written.
func lockBuyerQuota(ctx context.Context, tx pgx.Tx, eventID string, buyerNIKHash []byte) (buyerQuota, error) {
	var q buyerQuota
	err := tx.QueryRow(ctx, `
		WITH ev AS (SELECT status, max_tickets_per_buyer_nik FROM booking.events WHERE id=$1::uuid),
		     q AS (
		         INSERT INTO booking.buyer_quotas (event_id, buyer_nik_hash, max_quantity)
		         SELECT $1::uuid, $2, ev.max_tickets_per_buyer_nik FROM ev
		         ON CONFLICT (event_id, buyer_nik_hash) DO UPDATE SET updated_at = booking.buyer_quotas.updated_at
		         RETURNING held_quantity, paid_quantity, max_quantity)
		SELECT q.held_quantity, q.paid_quantity, q.max_quantity, ev.status FROM q CROSS JOIN ev`,
		eventID, buyerNIKHash).Scan(&q.held, &q.paid, &q.max, &q.eventStatus)
	return q, err
}

// checkCategoryStock locks the requested category rows in id order and refuses with inbound.CategoryUnavailable when
// any has fewer free units than requested. The typed error rolls back: the admission and its reserved units are
// untouched, so the buyer may pick another category.
func checkCategoryStock(ctx context.Context, tx pgx.Tx, byCat map[string]int) error {
	cats := make([]string, 0, len(byCat))
	for c := range byCat {
		cats = append(cats, c)
	}
	rows, err := tx.Query(ctx, `
		SELECT category_id::text, capacity - held_quantity - sold_quantity
		  FROM booking.category_inventories WHERE category_id = ANY($1::uuid[]) ORDER BY category_id FOR UPDATE`, cats)
	if err != nil {
		return err
	}
	avail := map[string]int{}
	for rows.Next() {
		var id string
		var a int
		if err := rows.Scan(&id, &a); err != nil {
			rows.Close()
			return err
		}
		avail[id] = a
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	var refused []string
	for c, want := range byCat {
		if avail[c] < want {
			refused = append(refused, c)
		}
	}
	if len(refused) > 0 {
		return inbound.CategoryUnavailable(refused)
	}
	return nil
}

// throttleSeconds is how long (whole seconds, at least 1) the buyer must wait before the next hold attempt once the
// refusal budget is spent, or 0 when the attempt may go ahead. The wait is a capped exponential backoff from the last
// refusal on the injected clock (spec.Now).
func throttleSeconds(spec outbound.HoldSpec, refusals int, lastRefusal *time.Time) int {
	if spec.RefusalBudget <= 0 || refusals < spec.RefusalBudget || lastRefusal == nil {
		return 0
	}
	wait := spec.ThrottleBase << min(refusals-spec.RefusalBudget, 20)
	if wait <= 0 || wait > spec.ThrottleMax {
		wait = spec.ThrottleMax
	}
	until := lastRefusal.Add(wait)
	if !spec.Now.Before(until) {
		return 0
	}
	secs := int(until.Sub(spec.Now).Seconds())
	if time.Duration(secs)*time.Second < until.Sub(spec.Now) {
		secs++
	}
	if secs < 1 {
		secs = 1
	}
	return secs
}

// appendHoldEvents appends BookingHeld, then BookingLocked when the admission locks at once, and returns the stream
// version the bookings row is written with.
func (u *UnitOfWork) appendHoldEvents(ctx context.Context, tx pgx.Tx, spec outbound.HoldSpec, locked bool, lockReason string) (int, error) {
	held := domevent.BookingHeld{
		BookingID: spec.BookingID, EventID: spec.EventID, AdmissionID: spec.AdmissionID,
		AmountMinor: spec.AmountMinor, ItemQuantity: len(spec.Lines), Items: itemSnapshots(spec.Lines),
	}
	if err := u.appendEvent(ctx, tx, spec.BookingID, 0, domevent.TypeBookingHeld, held, spec.CorrelationID); err != nil {
		return 0, err
	}
	if !locked {
		return 1, nil
	}
	lockedEv := domevent.BookingLocked{BookingID: spec.BookingID, Reason: lockReason}
	if err := u.appendEvent(ctx, tx, spec.BookingID, 1, domevent.TypeBookingLocked, lockedEv, spec.CorrelationID); err != nil {
		return 0, err
	}
	return 2, nil
}

// insertHeldBooking writes the booking and its items in one statement (the items reference the booking row inserted
// by the same statement) and returns the item ids in spec.Lines order.
func insertHeldBooking(ctx context.Context, tx pgx.Tx, spec outbound.HoldSpec, version int) ([]string, error) {
	n := len(spec.Lines)
	itemIDs := make([]string, 0, n)
	lineCats := make([]string, 0, n)
	linePrices := make([]int64, 0, n)
	lineNIKHash := make([][]byte, 0, n)
	lineNIKEnc := make([][]byte, 0, n)
	lineMasked := make([]string, 0, n)
	lineNames := make([]string, 0, n)
	for _, l := range spec.Lines {
		itemIDs = append(itemIDs, l.ItemID)
		lineCats = append(lineCats, l.CategoryID)
		linePrices = append(linePrices, l.UnitPriceMinor)
		lineNIKHash = append(lineNIKHash, l.AttendeeNIKHash)
		lineNIKEnc = append(lineNIKEnc, l.AttendeeNIKEnc)
		lineMasked = append(lineMasked, l.AttendeeNIKMasked)
		lineNames = append(lineNames, l.AttendeeName)
	}
	if _, err := tx.Exec(ctx, `
		WITH b AS (
			INSERT INTO booking.bookings (id, public_ref, event_id, admission_id, visitor_id, buyer_nik_hash,
				buyer_email_enc, status, amount_minor, item_quantity, payment_expires_at, version, account_id)
			VALUES ($1::uuid,$2,$3::uuid,$4::uuid,$5::uuid,$6,$7,'PENDING_PAYMENT',$8,$9,$10,$11, NULLIF($12,'')::uuid))
		INSERT INTO booking.booking_items (id, booking_id, event_id, category_id, unit_price_minor,
			attendee_nik_hash, attendee_nik_enc, attendee_nik_masked, attendee_name, item_status)
		SELECT u.id, $1::uuid, $3::uuid, u.category_id, u.price, u.nik_hash, u.nik_enc, u.nik_masked, u.name, 'HELD'
		  FROM unnest($13::uuid[], $14::uuid[], $15::bigint[], $16::bytea[], $17::bytea[], $18::text[], $19::text[])
		       AS u(id, category_id, price, nik_hash, nik_enc, nik_masked, name)`,
		spec.BookingID, spec.PublicRef, spec.EventID, spec.AdmissionID, spec.VisitorID, spec.BuyerNIKHash,
		spec.BuyerEmailEnc, spec.AmountMinor, n, spec.PaymentExpiresAt, version, spec.AccountID,
		itemIDs, lineCats, linePrices, lineNIKHash, lineNIKEnc, lineMasked, lineNames); err != nil {
		return nil, mapHoldErr(err)
	}
	return itemIDs, nil
}

// addHeldCounters updates the counters in one statement. Each category gains its held units and the buyer quota gains
// the n held units; the supply moves this admission's own outstanding units out of allocated and the n held units
// into held (F1/F2).
func addHeldCounters(ctx context.Context, tx pgx.Tx, spec outbound.HoldSpec, byCat map[string]int, outstanding int) error {
	catIDs, catCounts := catArrays(byCat)
	if _, err := tx.Exec(ctx, `
		WITH cat AS (
			UPDATE booking.category_inventories ci
			   SET held_quantity = ci.held_quantity + u.n, updated_at = now()
			  FROM unnest($1::uuid[], $2::int[]) AS u(category_id, n)
			 WHERE ci.category_id = u.category_id),
		     q AS (
			UPDATE booking.buyer_quotas
			   SET held_quantity = held_quantity + $3, updated_at = now()
			 WHERE event_id = $4::uuid AND buyer_nik_hash = $5)
		UPDATE booking.event_supply
		   SET allocated_units = allocated_units - $6, held_units = held_units + $3, version = version + 1, updated_at = now()
		 WHERE event_id = $4::uuid`,
		catIDs, catCounts, len(spec.Lines), spec.EventID, spec.BuyerNIKHash, outstanding); err != nil {
		return mapHoldErr(err)
	}
	return nil
}

// bindHoldAdmission binds the buyer to the admission and zeroes its reserved units: CONVERTED when the booking locks
// at once, EDITABLE otherwise.
func bindHoldAdmission(ctx context.Context, tx pgx.Tx, spec outbound.HoldSpec, locked bool, lockReason string) error {
	if locked {
		_, err := tx.Exec(ctx, `
			UPDATE booking.admissions
			   SET status='CONVERTED', lock_reason=$2, locked_at=now(), ended_at=now(), end_reason='CONVERTED',
			       outstanding_units=0, buyer_nik_hash=$3, buyer_nik_masked=NULLIF($4,''), buyer_email_enc=NULLIF($5,'')
			 WHERE id=$1::uuid`, spec.AdmissionID, lockReason, spec.BuyerNIKHash, spec.BuyerNIKMasked, spec.BuyerEmailEnc)
		return err
	}
	_, err := tx.Exec(ctx, `
		UPDATE booking.admissions
		   SET status='EDITABLE', outstanding_units=0, buyer_nik_hash=$2, buyer_nik_masked=NULLIF($3,''), buyer_email_enc=NULLIF($4,'')
		 WHERE id=$1::uuid`, spec.AdmissionID, spec.BuyerNIKHash, spec.BuyerNIKMasked, spec.BuyerEmailEnc)
	return err
}
