package postgres

import (
	"context"
	"encoding/hex"
	"time"

	"github.com/jackc/pgx/v5"

	domevent "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/event"
	vo "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/valueobject"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

// Settle moves held → sold and issues tickets, idempotently (guard status != PAID).
func (u *UnitOfWork) Settle(ctx context.Context, spec outbound.SettleSpec) (bool, error) {
	return inTx(ctx, u, func(tx pgx.Tx) (bool, error) { return u.settle(ctx, tx, spec) })
}

// settleBooking is the booking and admission state settle decides on, read under the admission and booking locks.
type settleBooking struct {
	eventID, admissionID, admStatus, queueEntryID string
	status                                        vo.BookingStatus
	version                                       int
	buyerHash                                     []byte
	amount                                        int64
	publicRef, emailEnc                           string
	paymentExpiresAt                              time.Time
}

// eventInfo is the event summary carried in booking.paid.
type eventInfo struct {
	name, venue string
	startsAt    time.Time
}

func (u *UnitOfWork) settle(ctx context.Context, tx pgx.Tx, spec outbound.SettleSpec) (bool, error) {
	// Step 1: plain read — discover eventID and admissionID (no lock yet, §9.0).
	var b settleBooking
	var err error
	if b.eventID, b.admissionID, err = bookingRefs(ctx, tx, spec.BookingID); err != nil {
		return false, err
	}

	// Step 2: lock admission first (§9.0: admissions before bookings).
	if err := tx.QueryRow(ctx, `
		SELECT status FROM booking.admissions WHERE id=$1::uuid FOR UPDATE`, b.admissionID).
		Scan(&b.admStatus); err != nil {
		return false, err
	}

	// Step 3: lock booking row and read full state.
	if err := tx.QueryRow(ctx, `
		SELECT status, version, buyer_nik_hash, amount_minor, public_ref, COALESCE(buyer_email_enc, ''), payment_expires_at
		  FROM booking.bookings WHERE id=$1::uuid FOR UPDATE`, spec.BookingID).
		Scan(&b.status, &b.version, &b.buyerHash, &b.amount, &b.publicRef, &b.emailEnc, &b.paymentExpiresAt); err != nil {
		return false, err
	}
	if b.status == vo.BookingPaid || b.status == vo.BookingReview {
		return false, nil // idempotent no-op (design §9.5: PAID/REVIEW → no-op, ack)
	}

	// Step 4: queue_entry_id (the admission is locked, so a plain read is safe).
	if err := tx.QueryRow(ctx, `
		SELECT queue_entry_id::text FROM booking.admissions WHERE id=$1::uuid`, b.admissionID).
		Scan(&b.queueEntryID); err != nil {
		return false, err
	}

	// Design §9.5: EXPIRED/CANCELLED → late payment arrived → REVIEW (no stock change needed).
	if b.status == vo.BookingExpired || b.status == vo.BookingCancelled {
		return false, u.reviewEndedBooking(ctx, tx, spec, b)
	}

	// Step 5: reads that need no supply lock (the booking and the admission are locked): the HELD units per category
	// and the event's name, venue and start.
	byCat, err := heldByCategory(ctx, tx, spec.BookingID)
	if err != nil {
		return false, err
	}
	var ev eventInfo
	if err := tx.QueryRow(ctx, `SELECT name, venue, starts_at FROM booking.events WHERE id=$1::uuid`, b.eventID).
		Scan(&ev.name, &ev.venue, &ev.startsAt); err != nil {
		return false, err
	}

	// Step 6: lock event_supply (§9.0: after booking, before quota/categories). Only the statements below run under it.
	if err := lockEventSupply(ctx, tx, b.eventID); err != nil {
		return false, err
	}

	// Design §9.5, Property 7: a settlement time known to be after payment_expires_at is late → REVIEW (release held
	// stock). An unknown time (zero: the consumer could not parse it) is timely (decision D1): the booking is still
	// payable, so its stock is still held, and the charge expires at the same deadline.
	// P2-8: a charge whose amount differs from the booking's amount is not the sale we hold stock for, so it takes the
	// same REVIEW path. A zero spec.AmountMinor means the amount is unknown and is not checked.
	late := !spec.SettlementTime.IsZero() && spec.SettlementTime.After(b.paymentExpiresAt)
	mismatch := spec.AmountMinor > 0 && spec.AmountMinor != b.amount
	if late || mismatch {
		return false, u.reviewHeldBooking(ctx, tx, spec, b, byCat)
	}

	// Happy path: timely payment → PAID + tickets.
	if err := u.settlePaid(ctx, tx, spec, b, byCat, ev); err != nil {
		return false, err
	}
	return true, nil
}

// reviewEndedBooking marks an EXPIRED/CANCELLED booking REVIEW: its stock was already released, so only the status,
// the event and the booking.review message change.
func (u *UnitOfWork) reviewEndedBooking(ctx context.Context, tx pgx.Tx, spec outbound.SettleSpec, b settleBooking) error {
	review := domevent.BookingReview{BookingID: spec.BookingID, AmountMinor: b.amount}
	if err := u.appendEvent(ctx, tx, spec.BookingID, b.version, domevent.TypeBookingReview, review, spec.CorrelationID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE booking.bookings SET status='REVIEW', version=$2, updated_at=now()
		 WHERE id=$1::uuid`, spec.BookingID, b.version+1); err != nil {
		return err
	}
	return enqueueOutboxMany(ctx, tx, []outboxRow{reviewRow(spec.BookingID, b.emailEnc, b.amount)})
}

// reviewHeldBooking handles a late or mismatched charge on a payable booking: the held stock is released and the
// booking goes to REVIEW with no tickets issued.
func (u *UnitOfWork) reviewHeldBooking(ctx context.Context, tx pgx.Tx, spec outbound.SettleSpec, b settleBooking, byCat map[string]int) error {
	review := domevent.BookingReview{BookingID: spec.BookingID, AmountMinor: b.amount}
	if err := u.appendEvent(ctx, tx, spec.BookingID, b.version, domevent.TypeBookingReview, review, spec.CorrelationID); err != nil {
		return err
	}
	if err := releaseHeldItems(ctx, tx, spec.BookingID, b.eventID, b.buyerHash, byCat); err != nil {
		return err
	}
	// settlement_time stays NULL when the gateway gave no time (mismatch without a settlement time).
	var settlementTime *time.Time
	if !spec.SettlementTime.IsZero() {
		settlementTime = &spec.SettlementTime
	}
	if _, err := tx.Exec(ctx, `
		UPDATE booking.bookings SET status='REVIEW', settlement_time=$2, version=$3, updated_at=now()
		 WHERE id=$1::uuid`, spec.BookingID, settlementTime, b.version+1); err != nil {
		return err
	}
	return enqueueOutboxMany(ctx, tx, []outboxRow{
		reviewRow(spec.BookingID, b.emailEnc, b.amount),
		journeyEndedRow(spec.BookingID, b.eventID, b.queueEntryID, "EXPIRED"),
	})
}

// settlePaid is the timely payment: the booking becomes PAID, its held units become sold, one ticket is issued per
// item, and booking.paid (plus sold_out / quota.exhausted when they apply) goes to the outbox.
func (u *UnitOfWork) settlePaid(ctx context.Context, tx pgx.Tx, spec outbound.SettleSpec, b settleBooking, byCat map[string]int, ev eventInfo) error {
	paidEv := domevent.BookingPaid{BookingID: spec.BookingID, AmountMinor: b.amount, Method: spec.Method}
	if err := u.appendEvent(ctx, tx, spec.BookingID, b.version, domevent.TypeBookingPaid, paidEv, spec.CorrelationID); err != nil {
		return err
	}
	now := time.Now().UTC()
	settledAt := spec.SettlementTime
	if settledAt.IsZero() {
		settledAt = now // unknown settlement time: the column records when Booking settled
	}
	if _, err := tx.Exec(ctx, `
		UPDATE booking.bookings SET status='PAID', paid_at=$2, settlement_time=$3, version=$4, updated_at=$2
		 WHERE id=$1::uuid`, spec.BookingID, now, settledAt, b.version+1); err != nil {
		return err
	}
	if err := issueTickets(ctx, tx, spec.BookingID, spec.TicketCodeFn); err != nil {
		return err
	}

	// Counters and the checks that read them, in one statement: the held units become sold (category, quota, supply).
	c, err := moveHeldCounters(ctx, tx, b.eventID, b.buyerHash, byCat, true)
	if err != nil {
		return err
	}
	var outbox []outboxRow
	// Publish sold_out only when nothing can come back (SRS-F-09-05): free + allocated + held = 0 ⟺ sold = total
	// with no allocation and no hold outstanding. Other buyers' reservations may still be released.
	if c.allocated+c.held == 0 && c.sold >= c.total {
		outbox = append(outbox, outboxRow{"booking.sold_out", spec.BookingID, map[string]any{"eventId": b.eventID}})
	}
	// Publish quota.exhausted if buyer reached max quota (OQ-13).
	if c.paid != nil && c.max != nil && *c.paid >= *c.max {
		outbox = append(outbox, outboxRow{"booking.quota.exhausted", spec.BookingID,
			map[string]any{"eventId": b.eventID, "buyerNikHash": hex.EncodeToString(b.buyerHash)}})
	}

	// §9.5: convert admission EDITABLE→CONVERTED(BOOKING_ENDED) if still open.
	// (paymentLock §9.4 normally converts it first; this handles the race where payment.succeeded
	// arrives before payment-lock was called.)
	if b.admStatus == "EDITABLE" {
		if err := endEditableAdmission(ctx, tx, b.eventID, b.admissionID); err != nil {
			return err
		}
	}

	ticketLinkToken := ""
	if spec.TicketLinkTokenFn != nil {
		ticketLinkToken = spec.TicketLinkTokenFn(spec.BookingID)
	}
	// Full booking.paid payload per schema (docs/contracts/events/booking.paid.v1.schema.json).
	paid := map[string]any{
		"bookingId":       spec.BookingID,
		"publicRef":       b.publicRef,
		"buyerEmailEnc":   b.emailEnc,
		"event":           map[string]any{"eventId": b.eventID, "name": ev.name, "venue": ev.venue, "startsAt": ev.startsAt.UTC().Format(time.RFC3339)},
		"tickets":         paidTicketLines(ctx, tx, spec.BookingID),
		"ticketLinkToken": ticketLinkToken,
	}
	outbox = append(outbox,
		outboxRow{"booking.paid", spec.BookingID, paid},
		journeyEndedRow(spec.BookingID, b.eventID, b.queueEntryID, "PAID"),
	)
	return enqueueOutboxMany(ctx, tx, outbox)
}

// issueTickets marks the booking's HELD items PAID and inserts one ticket per paid item (ON CONFLICT keeps it
// idempotent). codeFn supplies each ticket's public code.
func issueTickets(ctx context.Context, tx pgx.Tx, bookingID string, codeFn func(itemID string) string) error {
	rows, err := tx.Query(ctx, `
		UPDATE booking.booking_items SET item_status='PAID'
		 WHERE booking_id=$1::uuid AND item_status='HELD' RETURNING id::text`, bookingID)
	if err != nil {
		return err
	}
	itemIDs := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		itemIDs = append(itemIDs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	codes := make([]string, len(itemIDs))
	for i, id := range itemIDs {
		codes[i] = codeFn(id)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO booking.tickets (booking_id, booking_item_id, public_code)
		SELECT $1::uuid, u.item_id, u.code FROM unnest($2::uuid[], $3::text[]) AS u(item_id, code)
		ON CONFLICT (booking_item_id) DO NOTHING`, bookingID, itemIDs, codes)
	return err
}

// ticketLine is one entry of booking.paid's tickets array.
type ticketLine struct {
	TicketCode        string `json:"ticketCode"`
	AttendeeName      string `json:"attendeeName"`
	AttendeeNIKMasked string `json:"attendeeNikMasked"`
	CategoryCode      string `json:"categoryCode"`
}

// paidTicketLines reads the booking's tickets for the booking.paid payload. A read error leaves the list short
// instead of failing the settlement (kept as it was before the unit of work was split).
func paidTicketLines(ctx context.Context, tx pgx.Tx, bookingID string) []ticketLine {
	var lines []ticketLine
	rows, err := tx.Query(ctx, `
		SELECT t.public_code, bi.attendee_name, bi.attendee_nik_masked, tc.code
		  FROM booking.tickets t
		  JOIN booking.booking_items bi ON bi.id = t.booking_item_id
		  JOIN booking.ticket_categories tc ON tc.id = bi.category_id
		 WHERE t.booking_id = $1::uuid`, bookingID)
	if err != nil {
		return lines
	}
	defer rows.Close()
	for rows.Next() {
		var tl ticketLine
		_ = rows.Scan(&tl.TicketCode, &tl.AttendeeName, &tl.AttendeeNIKMasked, &tl.CategoryCode)
		lines = append(lines, tl)
	}
	return lines
}
