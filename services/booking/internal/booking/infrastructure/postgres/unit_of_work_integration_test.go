//go:build integration

package postgres

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/apperror"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/contract"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/migrate"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/deployments/migrations"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

const (
	testDB        = "rbt_it_booking"
	eventContract = "../../../../../../docs/contracts/events/"
)

// setup creates a throw-away database. Booking's SQL names the schema "booking" explicitly (queries and the supply
// trigger), so unlike the other services it cannot run in a temporary schema of the shared database.
func setup(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN tidak diset")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	_, err = admin.Exec(ctx, `DROP DATABASE IF EXISTS `+testDB+` WITH (FORCE)`)
	require.NoError(t, err)
	_, err = admin.Exec(ctx, `CREATE DATABASE `+testDB)
	require.NoError(t, err)

	cfg, err := pgxpool.ParseConfig(dsn)
	require.NoError(t, err)
	cfg.ConnConfig.Database = testDB
	cfg.ConnConfig.RuntimeParams["search_path"] = "booking"

	setupCfg := cfg.Copy()
	delete(setupCfg.ConnConfig.RuntimeParams, "search_path")
	root, err := pgxpool.NewWithConfig(ctx, setupCfg)
	require.NoError(t, err)
	_, err = root.Exec(ctx, `CREATE SCHEMA booking`)
	require.NoError(t, err)
	migs, err := migrate.Load(migrations.FS, ".")
	require.NoError(t, err)
	runner, err := migrate.NewRunner(root, "booking", migs, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)
	_, err = runner.Up(ctx, 0)
	require.NoError(t, err)
	root.Close()

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(context.Background(), `DROP DATABASE IF EXISTS `+testDB+` WITH (FORCE)`)
		admin.Close()
	})
	return pool
}

type fixture struct {
	pool                                   *pgxpool.Pool
	uow                                    *UnitOfWork
	eventID, categoryID, admissionID, nikH string
}

func (f fixture) q(t *testing.T, sql string, args ...any) string {
	t.Helper()
	var out string
	require.NoError(t, f.pool.QueryRow(context.Background(), sql, args...).Scan(&out))
	return out
}

func (f fixture) n(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var out int
	require.NoError(t, f.pool.QueryRow(context.Background(), sql, args...).Scan(&out))
	return out
}

const visitor = "22222222-2222-2222-2222-222222222222"

// sealedEmail stands in for the ciphertext Queue produces; Booking never opens it.
const sealedEmail = "c2VhbGVkLWVtYWls"

// seed creates one AVAILABLE event with 10 VIP tickets at Rp 150.000 and an ACTIVE admission for one unit.
func seed(t *testing.T) fixture {
	t.Helper()
	pool := setup(t)
	ctx := context.Background()
	f := fixture{pool: pool, nikH: "nik-hash-1"}
	uow, err := NewUnitOfWork(pool)
	require.NoError(t, err)
	f.uow = uow

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	require.NoError(t, tx.QueryRow(ctx, `
		INSERT INTO booking.events (slug, name, venue, starts_at, sales_starts_at, sales_ends_at, status, genre, published_at)
		VALUES ('konser-a', 'Konser A', 'GBK', '2026-12-25T19:00:00Z', now() - interval '1 hour', now() + interval '1 day', 'AVAILABLE', ARRAY['POP','ROCK'], now())
		RETURNING id::text`).Scan(&f.eventID))
	require.NoError(t, tx.QueryRow(ctx, `
		INSERT INTO booking.ticket_categories (event_id, code, name, price_minor) VALUES ($1::uuid, 'VIP', 'VIP', 150000) RETURNING id::text`,
		f.eventID).Scan(&f.categoryID))
	_, err = tx.Exec(ctx, `INSERT INTO booking.category_inventories (category_id, event_id, capacity) VALUES ($1::uuid, $2::uuid, 10)`, f.categoryID, f.eventID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `INSERT INTO booking.event_supply (event_id, total_capacity, allocated_units) VALUES ($1::uuid, 10, 1)`, f.eventID)
	require.NoError(t, err)
	require.NoError(t, tx.QueryRow(ctx, `
		INSERT INTO booking.admissions (event_id, queue_entry_id, queue_seq, visitor_id, buyer_nik_hash, buyer_email_enc, units, outstanding_units, status,
			cfg_selection_ttl_seconds, cfg_payment_ttl_seconds, cfg_submit_grace_seconds, cfg_edit_enabled, cfg_max_edits, cfg_allow_mixed_categories,
			claimed_at, expires_at)
		VALUES ($1::uuid, gen_random_uuid(), 1, $2::uuid, $3, 'c2VhbGVkLWVtYWls', 1, 1, 'ACTIVE', 300, 600, 5, true, 3, true, now(), now() + interval '5 minutes')
		RETURNING id::text`, f.eventID, visitor, []byte(f.nikH)).Scan(&f.admissionID))
	require.NoError(t, tx.Commit(ctx))
	return f
}

// hold makes a PENDING_PAYMENT booking through the real unit of work and returns its id.
func (f fixture) hold(t *testing.T) string {
	t.Helper()
	id := f.q(t, `SELECT gen_random_uuid()::text`)
	_, err := f.uow.Hold(context.Background(), outbound.HoldSpec{
		BookingID: id, PublicRef: "RB-7F3A9C21", EventID: f.eventID, AdmissionID: f.admissionID, VisitorID: visitor,
		BuyerNIKHash: []byte(f.nikH), BuyerEmailEnc: sealedEmail, AmountMinor: 150000, CfgEditEnabled: true, PaymentExpiresAt: time.Now().Add(10 * time.Minute),
		Lines: []outbound.HoldLine{{
			CategoryID: f.categoryID, UnitPriceMinor: 150000, AttendeeNIKHash: []byte("attendee-1"), AttendeeNIKEnc: []byte("enc"),
			AttendeeNIKMasked: "3174********0001", AttendeeName: "Budi Santoso",
		}},
		CorrelationID: id,
	})
	require.NoError(t, err)
	return id
}

// Regression: a hold that locks at once (edit disabled) appends BookingHeld + BookingLocked, so the read model must
// be at version 2; otherwise the next transition (payment-lock, settle, expire) hits an event-store version conflict.
func TestHold_LockedAtOnceKeepsReadModelInStepWithTheStream(t *testing.T) {
	f := seed(t)
	ctx := context.Background()
	id := f.q(t, `SELECT gen_random_uuid()::text`)
	res, err := f.uow.Hold(ctx, outbound.HoldSpec{
		BookingID: id, PublicRef: "RB-7F3A9C22", EventID: f.eventID, AdmissionID: f.admissionID, VisitorID: visitor,
		BuyerNIKHash: []byte(f.nikH), BuyerEmailEnc: sealedEmail, AmountMinor: 150000, CfgEditEnabled: false,
		PaymentExpiresAt: time.Now().Add(10 * time.Minute), CorrelationID: id,
		Lines: []outbound.HoldLine{{
			CategoryID: f.categoryID, UnitPriceMinor: 150000, AttendeeNIKHash: []byte("attendee-1"), AttendeeNIKEnc: []byte("enc"),
			AttendeeNIKMasked: "3174********0001", AttendeeName: "Budi Santoso",
		}},
	})
	require.NoError(t, err)
	assert.Equal(t, 2, res.Version)
	assert.Equal(t, 2, f.n(t, `SELECT version FROM booking.bookings WHERE id=$1::uuid`, id))

	lock, err := f.uow.PaymentLock(ctx, outbound.PaymentLockSpec{BookingID: id, AttemptKey: "attempt-1", CorrelationID: id})
	require.NoError(t, err)
	assert.Equal(t, "OK", lock.Outcome)
}

func (f fixture) status(t *testing.T, id string) string {
	return f.q(t, `SELECT status FROM booking.bookings WHERE id=$1::uuid`, id)
}

func (f fixture) stock(t *testing.T) [5]int {
	t.Helper()
	var s [5]int
	require.NoError(t, f.pool.QueryRow(context.Background(), `
		SELECT s.allocated_units, s.held_units, s.sold_units,
		       (SELECT held_quantity FROM booking.category_inventories WHERE event_id=s.event_id),
		       (SELECT paid_quantity FROM booking.buyer_quotas WHERE event_id=s.event_id)
		  FROM booking.event_supply s`).Scan(&s[0], &s[1], &s[2], &s[3], &s[4]))
	return s
}

func settleSpec(id string) outbound.SettleSpec {
	return outbound.SettleSpec{BookingID: id, Method: "VA_BCA", CorrelationID: id, TicketCodeFn: func(item string) string { return "TK-" + item[24:] }} // ekor acak: pada UUIDv7 awalan 8 karakter adalah stempel waktu dan bentrok
}

func (f fixture) outboxPayload(t *testing.T, routingKey string) json.RawMessage {
	t.Helper()
	var raw []byte
	require.NoError(t, f.pool.QueryRow(context.Background(), `SELECT payload FROM booking.outbox_events WHERE routing_key=$1`, routingKey).Scan(&raw))
	return raw
}

// F3/§9.4 K1: payment-lock stamps payment_started_at; booking status stays PENDING_PAYMENT (K1).
// The second call is idempotent: version and event count stay the same.
func TestPaymentLock_MovesToVerifyingAndIsIdempotent(t *testing.T) {
	f := seed(t)
	ctx := context.Background()
	id := f.hold(t)

	res, err := f.uow.PaymentLock(ctx, outbound.PaymentLockSpec{BookingID: id, AttemptKey: "attempt-1", CorrelationID: id})
	require.NoError(t, err)
	assert.Equal(t, "OK", res.Outcome)
	assert.EqualValues(t, 150000, res.AmountMinor)
	assert.Equal(t, 2, res.Version)
	// §9.4 K1: booking status stays PENDING_PAYMENT after payment-lock; only payment_started_at is set.
	assert.Equal(t, "PENDING_PAYMENT", f.status(t, id))
	assert.Equal(t, 1, f.n(t, `SELECT count(*) FROM booking.bookings WHERE id=$1::uuid AND payment_started_at IS NOT NULL`, id),
		"payment_started_at tercatat setelah payment-lock")

	again, err := f.uow.PaymentLock(ctx, outbound.PaymentLockSpec{BookingID: id, AttemptKey: "attempt-2", CorrelationID: id})
	require.NoError(t, err)
	assert.Equal(t, "OK", again.Outcome)
	assert.Equal(t, 2, again.Version, "lock kedua tidak mengubah versi")
	assert.Equal(t, 2, f.n(t, `SELECT count(*) FROM booking.booking_events WHERE stream_id=$1::uuid`, id), "BookingHeld + BookingLocked, tanpa event ganda")
}

func TestPaymentLock_RefusesExpiredAndNonPayableBookings(t *testing.T) {
	f := seed(t)
	ctx := context.Background()
	id := f.hold(t)
	_, err := f.pool.Exec(ctx, `UPDATE booking.bookings SET created_at = now() - interval '20 minutes', payment_expires_at = now() - interval '1 minute' WHERE id=$1::uuid`, id)
	require.NoError(t, err)
	res, err := f.uow.PaymentLock(ctx, outbound.PaymentLockSpec{BookingID: id, AttemptKey: "k", CorrelationID: id})
	require.NoError(t, err)
	assert.Equal(t, "PAYMENT_EXPIRED", res.Outcome)
	assert.Equal(t, "PENDING_PAYMENT", f.status(t, id), "booking yang kedaluwarsa tidak dikunci")

	_, err = f.pool.Exec(ctx, `UPDATE booking.bookings SET payment_expires_at = now() + interval '5 minutes', status='PAID', paid_at=now(), settlement_time=now() WHERE id=$1::uuid`, id)
	require.NoError(t, err)
	res, err = f.uow.PaymentLock(ctx, outbound.PaymentLockSpec{BookingID: id, AttemptKey: "k", CorrelationID: id})
	require.NoError(t, err)
	assert.Equal(t, "BOOKING_NOT_PAYABLE", res.Outcome)
}

// The extension is recorded as an event and only once; a second request is refused (deadline_extended_at).
func TestExtendDeadline_RecordsEventAndRefusesSecondExtension(t *testing.T) {
	f := seed(t)
	ctx := context.Background()
	id := f.hold(t)
	var before time.Time
	require.NoError(t, f.pool.QueryRow(ctx, `SELECT payment_expires_at FROM booking.bookings WHERE id=$1::uuid`, id).Scan(&before))

	require.NoError(t, f.uow.ExtendDeadline(ctx, outbound.DeadlineSpec{BookingID: id, Reason: "PAYMENT_PENDING", Seconds: 120, CorrelationID: id}))
	var after time.Time
	require.NoError(t, f.pool.QueryRow(ctx, `SELECT payment_expires_at FROM booking.bookings WHERE id=$1::uuid`, id).Scan(&after))
	assert.Equal(t, 120*time.Second, after.Sub(before))
	assert.Equal(t, 1, f.n(t, `SELECT count(*) FROM booking.booking_events WHERE stream_id=$1::uuid AND event_type='BookingDeadlineExtended'`, id))
	assert.Equal(t, 2, f.n(t, `SELECT version FROM booking.bookings WHERE id=$1::uuid`, id), "BookingHeld v1, BookingDeadlineExtended v2: read model mengikuti stream")

	err := f.uow.ExtendDeadline(ctx, outbound.DeadlineSpec{BookingID: id, Reason: "PAYMENT_PENDING", Seconds: 120, CorrelationID: id})
	assert.ErrorIs(t, err, inbound.ErrDeadlineAlreadyExtended)
	assert.Equal(t, 1, f.n(t, `SELECT count(*) FROM booking.booking_events WHERE stream_id=$1::uuid AND event_type='BookingDeadlineExtended'`, id), "tidak ada event ganda")
}

// D-B3: a VERIFYING booking is already past its deadline and must go through the sweeper, not be extended.
func TestExtendDeadline_RefusesVerifyingBooking(t *testing.T) {
	f := seed(t)
	ctx := context.Background()
	id := f.hold(t)
	_, err := f.pool.Exec(ctx, `UPDATE booking.bookings SET created_at = now() - interval '10 minutes', payment_expires_at = now() - interval '1 second' WHERE id=$1::uuid`, id)
	require.NoError(t, err)
	acted, err := f.uow.StartVerifying(ctx, id, 180)
	require.NoError(t, err)
	require.True(t, acted)

	err = f.uow.ExtendDeadline(ctx, outbound.DeadlineSpec{BookingID: id, Reason: "PAYMENT_PENDING", Seconds: 120, CorrelationID: id})
	assert.ErrorIs(t, err, inbound.ErrNotPayable)
	assert.Equal(t, 0, f.n(t, `SELECT count(*) FROM booking.booking_events WHERE stream_id=$1::uuid AND event_type='BookingDeadlineExtended'`, id))
	assert.Equal(t, 0, f.n(t, `SELECT count(*) FROM booking.bookings WHERE id=$1::uuid AND deadline_extended_at IS NOT NULL`, id))
}

func TestExtendDeadline_RefusesMissingAndLapsedBookings(t *testing.T) {
	f := seed(t)
	ctx := context.Background()

	err := f.uow.ExtendDeadline(ctx, outbound.DeadlineSpec{BookingID: "00000000-0000-0000-0000-000000000000", Reason: "PAYMENT_PENDING", Seconds: 120})
	assert.ErrorIs(t, err, inbound.ErrNotFound)

	id := f.hold(t)
	_, err = f.pool.Exec(ctx, `UPDATE booking.bookings SET created_at = now() - interval '10 minutes', payment_expires_at = now() - interval '1 second' WHERE id=$1::uuid`, id)
	require.NoError(t, err)
	err = f.uow.ExtendDeadline(ctx, outbound.DeadlineSpec{BookingID: id, Reason: "PAYMENT_PENDING", Seconds: 120, CorrelationID: id})
	assert.ErrorIs(t, err, inbound.ErrPaymentExpired)
	assert.Equal(t, 0, f.n(t, `SELECT count(*) FROM booking.booking_events WHERE stream_id=$1::uuid AND event_type='BookingDeadlineExtended'`, id))
}

// P1-5: a VERIFYING booking without payment_started_at is stamped by payment-lock (not returned as an idempotent no-op).
func TestPaymentLock_StampsAVerifyingBookingWithoutStamp(t *testing.T) {
	f := seed(t)
	ctx := context.Background()
	id := f.hold(t)
	_, err := f.pool.Exec(ctx, `UPDATE booking.bookings
		SET status='VERIFYING', verification_until = now() + interval '1 minute', payment_started_at = NULL,
		    payment_expires_at = now() + interval '1 minute'
		WHERE id=$1::uuid`, id)
	require.NoError(t, err)

	res, err := f.uow.PaymentLock(ctx, outbound.PaymentLockSpec{BookingID: id, AttemptKey: "attempt-1", CorrelationID: id})
	require.NoError(t, err)
	assert.Equal(t, "OK", res.Outcome)
	assert.Equal(t, 1, f.n(t, `SELECT count(*) FROM booking.bookings WHERE id=$1::uuid AND payment_started_at IS NOT NULL`, id))
}

// F5: booking.paid must carry everything the e-ticket email needs and follow the contract.
func TestSettle_PublishesATicketPayloadThatFollowsTheContract(t *testing.T) {
	f := seed(t)
	ctx := context.Background()
	id := f.hold(t)
	_, err := f.uow.PaymentLock(ctx, outbound.PaymentLockSpec{BookingID: id, AttemptKey: "k", CorrelationID: id})
	require.NoError(t, err)

	settled, err := f.uow.Settle(ctx, settleSpec(id))
	require.NoError(t, err)
	assert.True(t, settled)
	assert.Equal(t, "PAID", f.status(t, id))
	assert.Equal(t, [5]int{0, 0, 1, 0, 1}, f.stock(t), "allocated 0, held 0, sold 1, kategori held 0, kuota paid 1")

	raw := f.outboxPayload(t, "booking.paid")
	require.NoError(t, contract.ValidateFile(eventContract+"booking.paid.v1.schema.json", raw), "payload sesuai kontrak")
	var p struct {
		BookingID, PublicRef, BuyerEmailEnc string
		BuyerEmail                          *string
		Event                               struct{ EventID, Name, StartsAt, Venue string }
		Tickets                             []struct{ TicketCode, AttendeeName, AttendeeNikMasked, CategoryCode string }
	}
	require.NoError(t, json.Unmarshal(raw, &p))
	assert.Equal(t, id, p.BookingID)
	assert.Equal(t, "RB-7F3A9C21", p.PublicRef)
	assert.Equal(t, sealedEmail, p.BuyerEmailEnc, "email hanya sebagai ciphertext")
	assert.Nil(t, p.BuyerEmail, "tidak ada email plaintext di event")
	assert.NotContains(t, string(raw), "pembeli@contoh.id")
	assert.Equal(t, f.eventID, p.Event.EventID)
	assert.Equal(t, "Konser A", p.Event.Name)
	assert.Equal(t, "GBK", p.Event.Venue)
	assert.Equal(t, "2026-12-25T19:00:00Z", p.Event.StartsAt)
	require.Len(t, p.Tickets, 1)
	assert.Equal(t, "Budi Santoso", p.Tickets[0].AttendeeName)
	assert.Equal(t, "3174********0001", p.Tickets[0].AttendeeNikMasked, "NIK tersamar, bukan NIK lengkap")
	assert.Equal(t, "VIP", p.Tickets[0].CategoryCode)
	assert.Regexp(t, `^TK-`, p.Tickets[0].TicketCode)
	assert.NotContains(t, string(raw), "enc", "tidak ada NIK terenkripsi di payload")

	// Idempotent: a second settlement (duplicate payment.succeeded) changes nothing.
	again, err := f.uow.Settle(ctx, settleSpec(id))
	require.NoError(t, err)
	assert.False(t, again)
	assert.Equal(t, 1, f.n(t, `SELECT count(*) FROM booking.tickets`))
	assert.Equal(t, 1, f.n(t, `SELECT count(*) FROM booking.outbox_events WHERE routing_key='booking.paid'`))
	assert.Equal(t, [5]int{0, 0, 1, 0, 1}, f.stock(t))
}

// F6: a payment for a booking that already ended must not become a ticket or touch the stock.
func TestSettle_AfterExpiryGoesToReviewWithoutTicketsOrStockChange(t *testing.T) {
	f := seed(t)
	ctx := context.Background()
	id := f.hold(t)
	acted, err := f.uow.Expire(ctx, outbound.ExpireSpec{BookingID: id, Reason: "PAYMENT_TIMEOUT", CorrelationID: id})
	require.NoError(t, err)
	require.True(t, acted)
	require.Equal(t, "EXPIRED", f.status(t, id))
	stockAfterExpiry := f.stock(t)
	assert.Equal(t, [5]int{0, 0, 0, 0, 0}, stockAfterExpiry, "stok dilepas saat expire")

	settled, err := f.uow.Settle(ctx, settleSpec(id))
	require.NoError(t, err)
	assert.False(t, settled, "tidak ada settlement")
	assert.Equal(t, "REVIEW", f.status(t, id))
	assert.Equal(t, 0, f.n(t, `SELECT count(*) FROM booking.tickets`), "tanpa tiket")
	assert.Equal(t, stockAfterExpiry, f.stock(t), "stok tidak berubah")
	assert.Equal(t, 0, f.n(t, `SELECT count(*) FROM booking.outbox_events WHERE routing_key='booking.paid'`))

	raw := f.outboxPayload(t, "booking.review")
	require.NoError(t, contract.ValidateFile(eventContract+"booking.review.v1.schema.json", raw))
	assert.Contains(t, string(raw), id)

	// payment.succeeded delivered again: still REVIEW, still one booking.review.
	again, err := f.uow.Settle(ctx, settleSpec(id))
	require.NoError(t, err)
	assert.False(t, again)
	assert.Equal(t, 1, f.n(t, `SELECT count(*) FROM booking.outbox_events WHERE routing_key='booking.review'`))
}

// A late payment found while expiring (settled after the deadline) holds the booking in REVIEW; the payment.succeeded
// event that follows must not turn it into PAID.
func TestSettle_AfterAReviewDecisionIsANoOp(t *testing.T) {
	f := seed(t)
	ctx := context.Background()
	id := f.hold(t)
	acted, err := f.uow.Expire(ctx, outbound.ExpireSpec{BookingID: id, Reason: "PAYMENT_TIMEOUT", Review: true, CorrelationID: id})
	require.NoError(t, err)
	require.True(t, acted)
	require.Equal(t, "REVIEW", f.status(t, id))

	settled, err := f.uow.Settle(ctx, settleSpec(id))
	require.NoError(t, err)
	assert.False(t, settled)
	assert.Equal(t, "REVIEW", f.status(t, id))
	assert.Equal(t, 0, f.n(t, `SELECT count(*) FROM booking.tickets`))
}

// Regression: the session sweeper must be able to end an EDITABLE admission whose selection time lapsed. CONVERTED
// needs lock_reason in the same statement (admissions_check2); it used to fail on every tick, forever.
func TestExpireSession_EditableAdmissionBecomesConverted(t *testing.T) {
	f := seed(t)
	ctx := context.Background()
	id := f.hold(t)
	require.Equal(t, "EDITABLE", f.q(t, `SELECT status FROM booking.admissions WHERE id=$1::uuid`, f.admissionID))
	held := f.n(t, `SELECT held_units FROM booking.event_supply WHERE event_id=$1::uuid`, f.eventID)

	require.NoError(t, NewAdmissionRepo(f.pool).ExpireSession(ctx, f.admissionID, time.Now()))

	assert.Equal(t, "CONVERTED", f.q(t, `SELECT status FROM booking.admissions WHERE id=$1::uuid`, f.admissionID))
	assert.Equal(t, "SELECTION_EXPIRED", f.q(t, `SELECT lock_reason FROM booking.admissions WHERE id=$1::uuid`, f.admissionID))
	assert.Equal(t, held, f.n(t, `SELECT held_units FROM booking.event_supply WHERE event_id=$1::uuid`, f.eventID), "stok yang di-hold booking tidak dilepas")
	assert.Equal(t, "PENDING_PAYMENT", f.status(t, id), "booking tetap menunggu pembayaran")
	require.NoError(t, NewAdmissionRepo(f.pool).ExpireSession(ctx, f.admissionID, time.Now()), "idempoten")
}

// A config value outside the table limits is the caller's mistake (400), not an internal error.
func TestUpdateConfig_OutOfRangeValueIsAValidationError(t *testing.T) {
	f := seed(t)
	ttl := 30 // payment TTL minimum is 120 s
	err := NewEventWriteRepo(f.pool).UpdateConfig(context.Background(), f.eventID, dto.UpdateEventConfigRequest{PaymentTTLSeconds: &ttl})
	var ae *apperror.Error
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, http.StatusBadRequest, ae.HTTPStatus)
}

// AC-1..3 (audit P0-5): a late payment found while expiring goes to REVIEW and must give the held stock back exactly
// once, free the visitor (journey.ended), and a repeated expiry must do nothing.
func TestExpire_ReviewReleasesHeldStockOnce(t *testing.T) {
	f := seed(t)
	ctx := context.Background()
	id := f.hold(t)

	acted, err := f.uow.Expire(ctx, outbound.ExpireSpec{BookingID: id, Reason: "PAYMENT_TIMEOUT", Review: true, CorrelationID: id})
	require.NoError(t, err)
	require.True(t, acted)
	assert.Equal(t, "REVIEW", f.status(t, id))
	assert.Equal(t, [5]int{0, 0, 0, 0, 0}, f.stock(t), "stok ditahan dikembalikan saat REVIEW")
	assert.Equal(t, 0, f.n(t, `SELECT held_quantity FROM booking.buyer_quotas WHERE event_id=$1::uuid`, f.eventID), "kuota ditahan dikembalikan")
	assert.Equal(t, 0, f.n(t, `SELECT count(*) FROM booking.booking_items WHERE booking_id=$1::uuid AND item_status='HELD'`, id))
	assert.Equal(t, 1, f.n(t, `SELECT count(*) FROM booking.outbox_events WHERE routing_key='booking.journey.ended'`), "pengunjung dibebaskan")

	again, err := f.uow.Expire(ctx, outbound.ExpireSpec{BookingID: id, Reason: "PAYMENT_TIMEOUT", Review: true, CorrelationID: id})
	require.NoError(t, err)
	assert.False(t, again, "expiry kedua tidak boleh mengerjakan apa-apa")
	assert.Equal(t, [5]int{0, 0, 0, 0, 0}, f.stock(t), "stok tidak dilepas dua kali")
	assert.Equal(t, 1, f.n(t, `SELECT count(*) FROM booking.outbox_events WHERE routing_key='booking.review'`))
	assert.Equal(t, 1, f.n(t, `SELECT count(*) FROM booking.outbox_events WHERE routing_key='booking.journey.ended'`))
}

// P1-4: once payment has started (payment_started_at stamped by payment-lock), a cancel must be refused under the row
// lock: the booking stays PENDING_PAYMENT, no stock moves, and no expired event or outbox message is written.
func TestExpire_CancelAfterPaymentLockIsRefused(t *testing.T) {
	f := seed(t)
	ctx := context.Background()
	id := f.hold(t)
	held := f.stock(t)

	lock, err := f.uow.PaymentLock(ctx, outbound.PaymentLockSpec{BookingID: id, CorrelationID: id})
	require.NoError(t, err)
	require.Equal(t, "OK", lock.Outcome)

	acted, err := f.uow.Expire(ctx, outbound.ExpireSpec{BookingID: id, Reason: "CANCELLED", CorrelationID: id})
	require.ErrorIs(t, err, inbound.ErrBookingLocked)
	assert.False(t, acted)
	assert.Equal(t, "PENDING_PAYMENT", f.status(t, id))
	assert.Equal(t, held, f.stock(t), "stok tidak berubah saat pembatalan ditolak")
	assert.Equal(t, 0, f.n(t, `SELECT count(*) FROM booking.outbox_events WHERE routing_key='booking.expired'`))
}

// P2-8: a settlement whose amount differs from the booking's amount is treated like a late payment: REVIEW, held stock
// released once, visitor freed once, and a redelivered payment.succeeded changes nothing.
func TestSettle_AmountMismatchGoesToReviewOnce(t *testing.T) {
	f := seed(t)
	ctx := context.Background()
	id := f.hold(t)
	amount := f.n(t, `SELECT amount_minor FROM booking.bookings WHERE id=$1::uuid`, id)

	wrong := settleSpec(id)
	wrong.AmountMinor = int64(amount) + 1
	settled, err := f.uow.Settle(ctx, wrong)
	require.NoError(t, err)
	assert.False(t, settled)
	assert.Equal(t, "REVIEW", f.status(t, id))
	assert.Equal(t, 0, f.n(t, `SELECT count(*) FROM booking.tickets`), "tanpa tiket")
	assert.Equal(t, [5]int{0, 0, 0, 0, 0}, f.stock(t), "stok dilepas")
	assert.Equal(t, 0, f.n(t, `SELECT count(*) FROM booking.bookings WHERE id=$1::uuid AND settlement_time IS NOT NULL`, id), "tanpa waktu settlement: NULL, bukan nol")

	again, err := f.uow.Settle(ctx, wrong)
	require.NoError(t, err)
	assert.False(t, again)
	assert.Equal(t, "REVIEW", f.status(t, id))
	assert.Equal(t, 1, f.n(t, `SELECT count(*) FROM booking.outbox_events WHERE routing_key='booking.review'`))
	assert.Equal(t, 1, f.n(t, `SELECT count(*) FROM booking.outbox_events WHERE routing_key='booking.journey.ended'`), "pengunjung dibebaskan sekali")
	assert.Equal(t, [5]int{0, 0, 0, 0, 0}, f.stock(t), "stok tidak berubah")
}

// AC-4 (audit P0-4): the payment provider confirmed the charge but gave no settlement time. The booking is still
// payable, so it is treated as on time: PAID, tickets issued, held stock becomes sold, settlement_time is recorded.
func TestSettle_UnknownSettlementTimeIsTimely(t *testing.T) {
	f := seed(t)
	ctx := context.Background()
	id := f.hold(t)
	_, err := f.pool.Exec(ctx, `UPDATE booking.bookings SET created_at = now() - interval '20 minutes', payment_expires_at = now() - interval '1 minute' WHERE id=$1::uuid`, id)
	require.NoError(t, err)

	settled, err := f.uow.Settle(ctx, settleSpec(id)) // SettlementTime is zero: unknown
	require.NoError(t, err)
	assert.True(t, settled)
	assert.Equal(t, "PAID", f.status(t, id))
	assert.Equal(t, 1, f.n(t, `SELECT count(*) FROM booking.tickets`))
	assert.Equal(t, 1, f.n(t, `SELECT count(*) FROM booking.bookings WHERE id=$1::uuid AND settlement_time IS NOT NULL AND settlement_time > '2000-01-01'`, id))
	assert.Equal(t, [5]int{0, 0, 1, 0, 1}, f.stock(t), "ditahan menjadi terjual: allocated 0, held 0, sold 1, kategori held 0, kuota paid 1")
}

// AC-5 regression: a settlement time that is known and after the deadline still goes to REVIEW and releases the stock.
func TestSettle_KnownLateSettlementGoesToReviewAndReleases(t *testing.T) {
	f := seed(t)
	ctx := context.Background()
	id := f.hold(t)
	_, err := f.pool.Exec(ctx, `UPDATE booking.bookings SET created_at = now() - interval '20 minutes', payment_expires_at = now() - interval '2 minutes' WHERE id=$1::uuid`, id)
	require.NoError(t, err)

	late := settleSpec(id)
	late.SettlementTime = time.Now().Add(-1 * time.Minute)
	settled, err := f.uow.Settle(ctx, late)
	require.NoError(t, err)
	assert.False(t, settled)
	assert.Equal(t, "REVIEW", f.status(t, id))
	assert.Equal(t, 0, f.n(t, `SELECT count(*) FROM booking.tickets`))
	assert.Equal(t, [5]int{0, 0, 0, 0, 0}, f.stock(t), "stok dilepas")
	assert.Equal(t, 1, f.n(t, `SELECT count(*) FROM booking.outbox_events WHERE routing_key='booking.journey.ended'`))
}
