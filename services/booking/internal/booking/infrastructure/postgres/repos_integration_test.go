//go:build integration

package postgres

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/mapper"
	vo "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/valueobject"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

func TestEventRepo_ListFilterSortAndDetail(t *testing.T) {
	f := seed(t)
	ctx := context.Background()
	repo := NewEventRepo(f.pool)

	all, err := repo.ListEvents(ctx, dto.EventListQuery{Status: "ALL"})
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Equal(t, "Konser A", all[0].Name)
	assert.EqualValues(t, 150000, all[0].MinPriceMinor, "harga minimum dari kategori")
	assert.ElementsMatch(t, []string{"POP", "ROCK"}, all[0].Genres, "genre text[] ter-scan")

	// Filters (SRS-F-28): the seeded event is on sale, in Jakarta? no city set, starts 2026-12-25 19:00 UTC (= 26 Dec WIB).
	ids := func(q dto.EventListQuery) int {
		got, err := repo.ListEvents(ctx, q)
		require.NoError(t, err)
		return len(got)
	}
	assert.Equal(t, 1, ids(dto.EventListQuery{Status: "AVAILABLE"}), "dalam masa jual")
	assert.Equal(t, 0, ids(dto.EventListQuery{Status: "NOT_STARTED"}))
	assert.Equal(t, 0, ids(dto.EventListQuery{Status: "CLOSED"}))
	assert.Equal(t, 1, ids(dto.EventListQuery{Status: "ALL", Timeframe: "UPCOMING"}), "mulai hari ini dan seterusnya")
	assert.Equal(t, 0, ids(dto.EventListQuery{City: "Surabaya"}))
	for _, sort := range []string{"PRICE_ASC", "PRICE_DESC", "DATE_ASC"} {
		assert.Equal(t, 1, ids(dto.EventListQuery{Sort: sort}), sort)
	}

	// Genre filter (US-015): overlap match; empty = all. Seeded event is ARRAY['POP','ROCK'].
	assert.Equal(t, 1, ids(dto.EventListQuery{Genres: []string{"POP"}}), "overlap satu genre")
	assert.Equal(t, 1, ids(dto.EventListQuery{Genres: []string{"JAZZ", "ROCK"}}), "overlap salah satu dari beberapa")
	assert.Equal(t, 0, ids(dto.EventListQuery{Genres: []string{"JAZZ"}}), "tidak overlap -> tidak muncul")
	assert.Equal(t, 1, ids(dto.EventListQuery{Genres: nil}), "kosong -> semua")

	// Date range (sargable, half-open UTC). Seeded starts_at = 2026-12-25 19:00 UTC.
	dec := func(d int) *time.Time { t := time.Date(2026, 12, d, 0, 0, 0, 0, time.UTC); return &t }
	assert.Equal(t, 1, ids(dto.EventListQuery{StartUTC: dec(25), EndUTC: dec(26)}), "25 Des (UTC) termasuk")
	assert.Equal(t, 1, ids(dto.EventListQuery{StartUTC: dec(1), EndUTC: dec(26)}), "dalam rentang bulan")
	assert.Equal(t, 0, ids(dto.EventListQuery{StartUTC: dec(26)}), "mulai 26 Des -> event 25 Des di luar")
	assert.Equal(t, 0, ids(dto.EventListQuery{EndUTC: dec(25)}), "end eksklusif pada 25 Des -> event pukul 19:00 di luar")

	// DATE_DESC ordering (needs >1 event): add a second, later event.
	var second string
	require.NoError(t, f.pool.QueryRow(ctx, `
		INSERT INTO booking.events (slug, name, venue, starts_at, sales_starts_at, sales_ends_at, status, genre, published_at)
		VALUES ('konser-b', 'Konser B', 'GBK', '2026-12-31T19:00:00Z', now() - interval '1 hour', now() + interval '1 day', 'AVAILABLE', ARRAY['JAZZ'], now())
		RETURNING id::text`).Scan(&second))
	asc, err := repo.ListEvents(ctx, dto.EventListQuery{Sort: "DATE_ASC"})
	require.NoError(t, err)
	require.Len(t, asc, 2)
	assert.Equal(t, "Konser A", asc[0].Name, "DATE_ASC: yang lebih awal dulu")
	desc, err := repo.ListEvents(ctx, dto.EventListQuery{Sort: "DATE_DESC"})
	require.NoError(t, err)
	require.Len(t, desc, 2)
	assert.Equal(t, "Konser B", desc[0].Name, "DATE_DESC: yang lebih akhir dulu")
	// clean up the second event so later assertions see a single-event catalog
	_, err = f.pool.Exec(ctx, `DELETE FROM booking.events WHERE id=$1::uuid`, second)
	require.NoError(t, err)

	// City is an exact, case-insensitive match; THIS_WEEK/THIS_MONTH follow the calendar in WIB.
	_, err = f.pool.Exec(ctx, `UPDATE booking.events SET city='Jakarta' WHERE id=$1::uuid`, f.eventID)
	require.NoError(t, err)
	assert.Equal(t, 1, ids(dto.EventListQuery{City: "jakarta"}))
	assert.Equal(t, 0, ids(dto.EventListQuery{City: "jak"}), "bukan pencarian substring")
	_, err = f.pool.Exec(ctx, `UPDATE booking.events SET starts_at = now() + interval '1 hour'`)
	require.NoError(t, err)
	assert.Equal(t, 1, ids(dto.EventListQuery{Timeframe: "UPCOMING"}))
	assert.Equal(t, 1, ids(dto.EventListQuery{Timeframe: "THIS_MONTH"}), "satu jam lagi masih bulan ini (kecuali pergantian bulan)")
	_, err = f.pool.Exec(ctx, `UPDATE booking.events SET starts_at = now() + interval '400 days'`)
	require.NoError(t, err)
	assert.Equal(t, 0, ids(dto.EventListQuery{Timeframe: "THIS_WEEK"}))
	assert.Equal(t, 0, ids(dto.EventListQuery{Timeframe: "THIS_MONTH"}))
	assert.Equal(t, 1, ids(dto.EventListQuery{Timeframe: "UPCOMING"}))

	e, err := repo.GetByID(ctx, f.eventID)
	require.NoError(t, err)
	assert.Equal(t, f.eventID, e.ID)

	d, err := repo.GetDetail(ctx, f.eventID)
	require.NoError(t, err)
	require.Len(t, d.Categories, 1)
	assert.Equal(t, "VIP", d.Categories[0].Category.Code)
	assert.Equal(t, 10, d.Categories[0].Available)

	_, err = repo.GetByID(ctx, "00000000-0000-0000-0000-000000000000")
	assert.ErrorIs(t, err, inbound.ErrNotFound)
	_, err = repo.GetDetail(ctx, "bukan-uuid")
	assert.ErrorIs(t, err, inbound.ErrNotFound)
}

func TestBookingRepo_GetListAndDue(t *testing.T) {
	f := seed(t)
	ctx := context.Background()
	id := f.hold(t)
	repo := NewBookingRepo(f.pool)

	b, err := repo.GetByID(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "RB-7F3A9C21", b.PublicRef)
	require.Len(t, b.Items, 1)

	_, err = repo.GetByID(ctx, "00000000-0000-0000-0000-000000000000")
	assert.ErrorIs(t, err, inbound.ErrNotFound)
	_, err = repo.GetByID(ctx, "bukan-uuid")
	assert.ErrorIs(t, err, inbound.ErrNotFound)

	due, err := repo.DueBookingIDs(ctx, time.Now(), 10)
	require.NoError(t, err)
	assert.Empty(t, due, "belum lewat batas bayar")
	_, err = f.pool.Exec(ctx, `UPDATE booking.bookings SET created_at = now() - interval '2 hours', payment_expires_at = now() - interval '1 minute' WHERE id=$1::uuid`, id)
	require.NoError(t, err)
	due, err = repo.DueBookingIDs(ctx, time.Now(), 10)
	require.NoError(t, err)
	assert.Equal(t, []string{id}, due)

	// History is per account, newest first, with a cursor.
	acct := "33333333-3333-3333-3333-333333333333"
	_, err = f.pool.Exec(ctx, `UPDATE booking.bookings SET account_id=$2::uuid WHERE id=$1::uuid`, id, acct)
	require.NoError(t, err)
	items, next, err := repo.ListByAccount(ctx, acct, "", 10)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, id, items[0].BookingID)
	assert.Equal(t, "Konser A", items[0].EventName)
	assert.Empty(t, next)
	other, _, err := repo.ListByAccount(ctx, "44444444-4444-4444-4444-444444444444", "", 10)
	require.NoError(t, err)
	assert.Empty(t, other, "akun lain tidak melihat pesanan ini")
}

// P3-6: bookings that share created_at must all be listed across pages (keyset on created_at, id), and an old
// timestamp-only cursor still pages from that timestamp.
func TestBookingRepo_ListByAccountSameCreatedAtIsNotSkipped(t *testing.T) {
	f := seed(t)
	ctx := context.Background()
	repo := NewBookingRepo(f.pool)
	acct := "33333333-3333-3333-3333-333333333333"
	created := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	want := map[string]bool{}
	for i := 0; i < 3; i++ {
		id := f.insertBooking(t, "EXPIRED", time.Now().Add(time.Hour), nil)
		_, err := f.pool.Exec(ctx, `UPDATE booking.bookings SET account_id=$2::uuid, created_at=$3, payment_expires_at=$4 WHERE id=$1::uuid`,
			id, acct, created, created.Add(time.Hour))
		require.NoError(t, err)
		want[id] = true
	}

	page1, next, err := repo.ListByAccount(ctx, acct, "", 2)
	require.NoError(t, err)
	require.Len(t, page1, 2)
	require.NotEmpty(t, next)
	page2, next2, err := repo.ListByAccount(ctx, acct, next, 2)
	require.NoError(t, err)
	require.Len(t, page2, 1)
	assert.Empty(t, next2)

	seen := map[string]bool{}
	for _, it := range append(page1, page2...) {
		assert.False(t, seen[it.BookingID], "id berulang di halaman: %s", it.BookingID)
		seen[it.BookingID] = true
	}
	assert.Equal(t, want, seen)

	old, _, err := repo.ListByAccount(ctx, acct, created.Format(time.RFC3339Nano), 10)
	require.NoError(t, err)
	assert.Len(t, old, 3, "cursor format lama (tanpa id) tetap memuat semua booking pada stempel waktu itu")
	junk, _, err := repo.ListByAccount(ctx, acct, created.Format(time.RFC3339Nano)+"_bukan-uuid", 10)
	require.NoError(t, err, "cursor dengan id rusak tidak boleh menjadi error database")
	assert.Len(t, junk, 3)
}

// insertBooking adds a booking in the given status without the hold path. admission_id is unique and an active
// admission per visitor is unique too, so each booking gets its own terminal (EXPIRED) admission. A nil deadline is NULL.
func (f fixture) insertBooking(t *testing.T, status string, paymentExpiresAt time.Time, verificationUntil *time.Time) string {
	t.Helper()
	admission := f.q(t, `
		INSERT INTO booking.admissions (event_id, queue_entry_id, queue_seq, visitor_id, buyer_nik_hash, buyer_email_enc, buyer_nik_masked,
			units, status, cfg_selection_ttl_seconds, cfg_payment_ttl_seconds, cfg_submit_grace_seconds, cfg_edit_enabled, cfg_max_edits,
			cfg_allow_mixed_categories, expires_at, ended_at, end_reason)
		VALUES ($1::uuid, gen_random_uuid(), 1, $2::uuid, $3, 'c2VhbGVkLWVtYWls', '3174********0001',
			1, 'EXPIRED', 300, 600, 5, true, 3, true, now() + interval '5 minutes', now(), 'SESSION_EXPIRED')
		RETURNING id::text`, f.eventID, visitor, []byte(f.nikH))
	return f.q(t, `
		INSERT INTO booking.bookings (public_ref, event_id, admission_id, visitor_id, buyer_nik_hash, buyer_email_enc, status,
			amount_minor, item_quantity, created_at, payment_expires_at, verification_until)
		VALUES ('RB-' || substr(gen_random_uuid()::text, 1, 8), $1::uuid, $2::uuid, $3::uuid, $4, 'c2VhbGVkLWVtYWls', $5,
			150000, 1, $6, $7, $8)
		RETURNING id::text`, f.eventID, admission, visitor, []byte(f.nikH), status,
		time.Now().Add(-2*time.Hour), paymentExpiresAt, verificationUntil)
}

// D-B5: a VERIFYING booking inside its grace window (verification_until is its next check) is not due, even when its
// payment deadline lapsed long ago. The sweeper takes only the rows that are actually due.
func TestBookingRepo_DueSkipsVerifyingInGrace(t *testing.T) {
	f := seed(t)
	ctx := context.Background()
	repo := NewBookingRepo(f.pool)
	now := time.Now()

	for range 3 {
		f.insertBooking(t, "VERIFYING", now.Add(-10*time.Minute), ptrTime(now.Add(time.Minute)))
	}
	pending := f.insertBooking(t, "PENDING_PAYMENT", now.Add(-time.Second), nil)

	due, err := repo.DueBookingIDs(ctx, now, 2)
	require.NoError(t, err)
	assert.Equal(t, []string{pending}, due, "limit 2 tidak boleh habis oleh VERIFYING yang masih dalam grace")

	// Once its grace window has passed, a VERIFYING booking is due again.
	_, err = f.pool.Exec(ctx, `UPDATE booking.bookings SET verification_until = now() - interval '1 second' WHERE status='VERIFYING' AND id IN (
		SELECT id FROM booking.bookings WHERE status='VERIFYING' LIMIT 1)`)
	require.NoError(t, err)
	due, err = repo.DueBookingIDs(ctx, time.Now(), 10)
	require.NoError(t, err)
	assert.Len(t, due, 2, "PENDING yang lewat batas bayar dan satu VERIFYING yang grace-nya sudah lewat")
	assert.Contains(t, due, pending)
}

func ptrTime(t time.Time) *time.Time { return &t }

// D-B5: DeferVerification moves only a VERIFYING booking's next check; the deferred row is then not due until that time.
func TestBookingRepo_DeferVerificationOnlyMovesVerifying(t *testing.T) {
	f := seed(t)
	ctx := context.Background()
	repo := NewBookingRepo(f.pool)
	now := time.Now()
	verifying := f.insertBooking(t, "VERIFYING", now.Add(-10*time.Minute), ptrTime(now.Add(-time.Second)))
	pending := f.insertBooking(t, "PENDING_PAYMENT", now.Add(-time.Second), nil)

	until := now.Add(30 * time.Second)
	require.NoError(t, repo.DeferVerification(ctx, verifying, until))
	require.NoError(t, repo.DeferVerification(ctx, pending, until), "bukan VERIFYING: dibiarkan")

	var got time.Time
	require.NoError(t, f.pool.QueryRow(ctx, `SELECT verification_until FROM booking.bookings WHERE id=$1::uuid`, verifying).Scan(&got))
	assert.WithinDuration(t, until, got, time.Millisecond)
	assert.Equal(t, 1, f.n(t, `SELECT count(*) FROM booking.bookings WHERE id=$1::uuid AND verification_until IS NULL`, pending))

	due, err := repo.DueBookingIDs(ctx, now, 10)
	require.NoError(t, err)
	assert.Equal(t, []string{pending}, due, "VERIFYING yang ditunda belum jatuh tempo")
}

// Deferral only moves the next check later: a check already scheduled further out (the grace end) is kept, so a
// backoff after UNKNOWN cannot shorten the grace window that protects late webhooks.
func TestDeferVerification_NeverMovesTheCheckEarlier(t *testing.T) {
	f := seed(t)
	ctx := context.Background()
	repo := NewBookingRepo(f.pool)
	now := time.Now()
	id := f.insertBooking(t, "VERIFYING", now.Add(-10*time.Minute), ptrTime(now.Add(3*time.Minute)))
	untilOf := func() time.Time {
		var got time.Time
		require.NoError(t, f.pool.QueryRow(ctx, `SELECT verification_until FROM booking.bookings WHERE id=$1::uuid`, id).Scan(&got))
		return got
	}

	require.NoError(t, repo.DeferVerification(ctx, id, now.Add(30*time.Second)))
	assert.WithinDuration(t, now.Add(3*time.Minute), untilOf(), time.Millisecond, "penundaan yang lebih awal dari akhir grace diabaikan")

	require.NoError(t, repo.DeferVerification(ctx, id, now.Add(10*time.Minute)))
	assert.WithinDuration(t, now.Add(10*time.Minute), untilOf(), time.Millisecond, "pemeriksaan yang lebih jauh diterapkan")
}

func TestAdmissionRepo_FindActivateReserveReleaseAndDue(t *testing.T) {
	f := seed(t)
	ctx := context.Background()
	repo := NewAdmissionRepo(f.pool)

	a, err := repo.GetByID(ctx, f.admissionID)
	require.NoError(t, err)
	assert.Equal(t, f.eventID, a.EventID)
	_, err = repo.GetByID(ctx, "00000000-0000-0000-0000-000000000000")
	require.Error(t, err)

	act, err := repo.FindActiveByVisitor(ctx, f.eventID, visitor)
	require.NoError(t, err)
	assert.Equal(t, f.admissionID, act.ID)
	byEvent, err := repo.FindActiveByEvent(ctx, f.eventID, visitor)
	require.NoError(t, err)
	assert.Equal(t, f.admissionID, byEvent.ID)

	// A RESERVED admission (as ReserveBatch leaves it) is activated once by its admission token's jti.
	jti := f.q(t, `SELECT gen_random_uuid()::text`)
	_, err = f.pool.Exec(ctx, `UPDATE booking.admissions SET status='RESERVED', admission_jti=$2::uuid, claimed_at=NULL WHERE id=$1::uuid`, f.admissionID, jti)
	require.NoError(t, err)
	activated, err := repo.Activate(ctx, f.admissionID, time.Now()) // the token's jti is the admission id (allocationId)
	require.NoError(t, err)
	assert.Equal(t, "ACTIVE", string(activated.Status))
	_, err = repo.Activate(ctx, f.admissionID, time.Now())
	assert.ErrorIs(t, err, inbound.ErrAdmissionToken, "admission token sekali pakai")

	// Sessions past their time are due; ending an ACTIVE one frees its stock.
	_, err = f.pool.Exec(ctx, `UPDATE booking.admissions SET reserved_at = now() - interval '3 hours', claimed_at = now() - interval '2 hours', expires_at = now() - interval '1 hour' WHERE id=$1::uuid`, f.admissionID)
	require.NoError(t, err)
	due, err := repo.DueSessions(ctx, time.Now(), 10)
	require.NoError(t, err)
	require.Len(t, due, 1)
	require.NoError(t, repo.ExpireSession(ctx, f.admissionID, time.Now()))
	assert.Equal(t, 0, f.n(t, `SELECT allocated_units FROM booking.event_supply WHERE event_id=$1::uuid`, f.eventID), "stok dialokasi dilepas")
	assert.Equal(t, 1, f.n(t, `SELECT count(*) FROM booking.outbox_events WHERE routing_key='booking.journey.ended'`))
}

func TestAdmissionRepo_ReserveBatchGrantsInOrderThenRunsOutOfSupply(t *testing.T) {
	f := seed(t)
	ctx := context.Background()
	repo := NewAdmissionRepo(f.pool)
	_, err := f.pool.Exec(ctx, `ALTER TABLE booking.events DISABLE TRIGGER USER`) // rules are locked once sales started
	require.NoError(t, err)
	_, err = f.pool.Exec(ctx, `UPDATE booking.events SET max_tickets_per_order=1 WHERE id=$1::uuid`, f.eventID)
	require.NoError(t, err)
	_, err = f.pool.Exec(ctx, `UPDATE booking.event_supply SET allocated_units=8 WHERE event_id=$1::uuid`, f.eventID)
	require.NoError(t, err)
	_, err = f.pool.Exec(ctx, `DELETE FROM booking.admissions`)
	require.NoError(t, err)

	entry := func(n int) outbound.ReserveEntry {
		return outbound.ReserveEntry{
			QueueEntryID: f.q(t, `SELECT gen_random_uuid()::text`), Seq: int64(n), VisitorID: f.q(t, `SELECT gen_random_uuid()::text`),
			BuyerNIKHash: []byte{byte(n)}, BuyerNIKMasked: "3174********0001", BuyerEmailEnc: sealedEmail, Units: 1,
			AdmissionJTI: f.q(t, `SELECT gen_random_uuid()::text`),
		}
	}
	entries := []outbound.ReserveEntry{entry(1), entry(2), entry(3)}
	out, err := repo.ReserveBatch(ctx, f.eventID, entries, time.Now())
	require.NoError(t, err)
	require.Len(t, out, 3)
	assert.Equal(t, "GRANTED", out[0].Outcome)
	assert.Equal(t, "GRANTED", out[1].Outcome)
	assert.Equal(t, "NO_SUPPLY_NOW", out[2].Outcome, "stok habis: yang di belakang tidak diadmit")
	assert.NotEmpty(t, out[0].AllocationID)

	// The same queue entry asked again (stock is now fully allocated) gets its own grant back: no second allocation, and
	// a Promotor that failed to promote it last tick can still do so.
	again, err := repo.ReserveBatch(ctx, f.eventID, entries[:1], time.Now())
	require.NoError(t, err)
	assert.Equal(t, "GRANTED", again[0].Outcome)
	assert.Equal(t, out[0].AllocationID, again[0].AllocationID)
	require.NotNil(t, again[0].ClaimDeadline)
	_, err = f.pool.Exec(ctx, `UPDATE booking.admissions SET status='ACTIVE', claimed_at=now() WHERE id=$1::uuid`, out[0].AllocationID)
	require.NoError(t, err)
	claimed, err := repo.ReserveBatch(ctx, f.eventID, entries[:1], time.Now())
	require.NoError(t, err)
	assert.Equal(t, "ALREADY_ACTIVE", claimed[0].Outcome, "sudah diklaim: bukan grant baru")
	assert.Equal(t, 10, f.n(t, `SELECT allocated_units FROM booking.event_supply WHERE event_id=$1::uuid`, f.eventID))

	released, err := repo.Release(ctx, out[1].AllocationID, vo.EndUnclaimed)
	require.NoError(t, err)
	assert.True(t, released)
	assert.Equal(t, 9, f.n(t, `SELECT allocated_units FROM booking.event_supply WHERE event_id=$1::uuid`, f.eventID))
	released, err = repo.Release(ctx, out[1].AllocationID, vo.EndUnclaimed)
	require.NoError(t, err)
	assert.False(t, released, "melepas dua kali tidak mengubah apa pun")
}

func TestOutboxWriter_ClaimFailPublish(t *testing.T) {
	f := seed(t)
	ctx := context.Background()
	id := f.hold(t)
	_, err := f.uow.Settle(ctx, settleSpec(id))
	require.NoError(t, err)
	w := NewOutboxWriter(f.pool)

	rows, err := w.Claim(ctx, 50, 30*time.Second)
	require.NoError(t, err)
	require.NotEmpty(t, rows)
	first := rows[0]

	require.NoError(t, w.MarkFailed(ctx, first.ID, 1, strings.Repeat("x", 600), time.Minute))
	// The claim leased every row for 30 s and the failed row now waits out its backoff: nothing is due again yet.
	rows, err = w.Claim(ctx, 50, 30*time.Second)
	require.NoError(t, err)
	for _, r := range rows {
		assert.NotEqual(t, first.ID, r.ID, "baris gagal ditunda sampai backoff lewat")
	}
	assert.Empty(t, rows, "baris yang sudah di-claim tidak diambil lagi sampai lease lewat")
	var lastErr string
	require.NoError(t, f.pool.QueryRow(ctx, `SELECT last_error FROM booking.outbox_events WHERE id=$1::uuid`, first.ID).Scan(&lastErr))
	assert.Len(t, lastErr, 512, "pesan error dipotong")

	_, err = f.pool.Exec(ctx, `UPDATE booking.outbox_events SET available_at=now() WHERE id=$1::uuid`, first.ID)
	require.NoError(t, err)
	require.NoError(t, w.MarkPublished(ctx, first.ID))
	assert.Equal(t, 0, f.n(t, `SELECT count(*) FROM booking.outbox_events WHERE id=$1::uuid AND published_at IS NULL`, first.ID))
}

// Two relay replicas claiming at once never get the same row: the claim leases rows (available_at moves ahead) under
// FOR UPDATE SKIP LOCKED, so the second claim skips what the first one took.
func TestOutboxClaim_TwoRelaysNeverGetTheSameRow(t *testing.T) {
	f := seed(t)
	ctx := context.Background()
	_, err := f.pool.Exec(ctx, `
		INSERT INTO booking.outbox_events (routing_key, aggregate_id, schema_version, payload)
		SELECT 'booking.test', gen_random_uuid(), 1, '{"a":1}'::jsonb FROM generate_series(1, 10)`)
	require.NoError(t, err)
	w := NewOutboxWriter(f.pool)

	var wg sync.WaitGroup
	var claimed [2][]string
	var errs [2]error
	for i := range claimed {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rows, err := w.Claim(ctx, 10, 30*time.Second)
			errs[i] = err
			for _, r := range rows {
				claimed[i] = append(claimed[i], r.ID)
			}
		}(i)
	}
	wg.Wait()
	require.NoError(t, errs[0])
	require.NoError(t, errs[1])

	seen := map[string]int{}
	for _, ids := range claimed {
		for _, id := range ids {
			seen[id]++
		}
	}
	assert.Len(t, seen, 10, "kedua relay bersama-sama mendapat seluruh 10 baris")
	for id, n := range seen {
		assert.Equal(t, 1, n, "baris %s diambil dua relay", id)
	}
}

func TestIdempotencyStore_BeginCompleteReplay(t *testing.T) {
	f := seed(t)
	ctx := context.Background()
	s := NewIdempotencyStore(f.pool)
	hash := []byte("h1")

	rec, found, err := s.Begin(ctx, "hold", visitor, "key-0123456789abcdef", hash, time.Hour)
	require.NoError(t, err)
	assert.False(t, found)
	assert.Equal(t, "IN_PROGRESS", rec.State)

	rec, found, err = s.Begin(ctx, "hold", visitor, "key-0123456789abcdef", hash, time.Hour)
	require.NoError(t, err)
	assert.True(t, found, "kunci yang sama sedang berjalan")
	assert.Equal(t, "IN_PROGRESS", rec.State)

	require.NoError(t, s.Complete(ctx, "hold", visitor, "key-0123456789abcdef", 201, []byte(`{"ok":true}`)))
	rec, found, err = s.Begin(ctx, "hold", visitor, "key-0123456789abcdef", hash, time.Hour)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "COMPLETED", rec.State)
	assert.Equal(t, 201, rec.ResponseStatus)
	assert.JSONEq(t, `{"ok":true}`, string(rec.ResponseBody))

	_, found, err = s.Begin(ctx, "hold", visitor, "key-lain-0123456789ab", hash, time.Hour)
	require.NoError(t, err)
	assert.False(t, found, "kunci berbeda adalah permintaan baru")
}

func TestIdempotencyBegin_StaleInProgressIsTakenOver(t *testing.T) {
	f := seed(t)
	ctx := context.Background()
	s := NewIdempotencyStore(f.pool)
	age := func(t *testing.T, key string) {
		t.Helper()
		_, err := f.pool.Exec(ctx, `
			UPDATE booking.idempotency_records SET created_at = now() - interval '2 minutes'
			 WHERE scope='hold' AND owner_id=$1::uuid AND idempotency_key=$2`, visitor, key)
		require.NoError(t, err)
	}

	t.Run("IN_PROGRESS baru tetap dikunci", func(t *testing.T) {
		const key = "key-baru-0123456789abcd"
		_, found, err := s.Begin(ctx, "hold", visitor, key, []byte("h1"), time.Hour)
		require.NoError(t, err)
		assert.False(t, found)

		rec, found, err := s.Begin(ctx, "hold", visitor, key, []byte("h2"), time.Hour)
		require.NoError(t, err)
		assert.True(t, found, "IN_PROGRESS kurang dari 60 detik tidak boleh diambil alih")
		assert.Equal(t, "IN_PROGRESS", rec.State)
	})

	t.Run("IN_PROGRESS lebih dari 60 detik diambil alih", func(t *testing.T) {
		const key = "key-stale-0123456789abc"
		_, found, err := s.Begin(ctx, "hold", visitor, key, []byte("h1"), time.Hour)
		require.NoError(t, err)
		assert.False(t, found)
		age(t, key)

		rec, found, err := s.Begin(ctx, "hold", visitor, key, []byte("h2"), time.Hour)
		require.NoError(t, err)
		assert.False(t, found, "IN_PROGRESS basi dianggap permintaan baru")
		assert.Equal(t, "IN_PROGRESS", rec.State)
		assert.Equal(t, []byte("h2"), rec.RequestHash)
	})

	t.Run("COMPLETED basi tidak diambil alih", func(t *testing.T) {
		const key = "key-selesai-0123456789ab"
		_, found, err := s.Begin(ctx, "hold", visitor, key, []byte("h1"), time.Hour)
		require.NoError(t, err)
		assert.False(t, found)
		require.NoError(t, s.Complete(ctx, "hold", visitor, key, 201, []byte(`{"ok":true}`)))
		age(t, key)

		rec, found, err := s.Begin(ctx, "hold", visitor, key, []byte("h2"), time.Hour)
		require.NoError(t, err)
		assert.True(t, found, "COMPLETED tidak pernah diambil alih oleh lease")
		assert.Equal(t, "COMPLETED", rec.State)
		assert.Equal(t, 201, rec.ResponseStatus)
		assert.JSONEq(t, `{"ok":true}`, string(rec.ResponseBody))
	})
}

func TestTicketQuotaAndAttendeeRepos(t *testing.T) {
	f := seed(t)
	ctx := context.Background()
	id := f.hold(t)

	held, paid, max, err := NewQuotaRepo(f.pool).CountHeldAndPaid(ctx, f.eventID, []byte(f.nikH))
	require.NoError(t, err)
	assert.Equal(t, 1, held)
	assert.Equal(t, 0, paid)
	assert.Equal(t, 3, max)
	h0, p0, m0, err := NewQuotaRepo(f.pool).CountHeldAndPaid(ctx, f.eventID, []byte("belum-pernah"))
	require.NoError(t, err)
	assert.Equal(t, [3]int{0, 0, 3}, [3]int{h0, p0, m0}, "pembeli tanpa baris kuota")

	tickets := NewTicketRepo(f.pool)
	none, err := tickets.ListByBookingID(ctx, id)
	require.NoError(t, err)
	assert.Empty(t, none, "belum dibayar: belum ada tiket")
	_, err = f.uow.Settle(ctx, settleSpec(id))
	require.NoError(t, err)
	got, err := tickets.ListByBookingID(ctx, id)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "VIP", got[0].CategoryCode)
	assert.Equal(t, "Budi Santoso", got[0].AttendeeName)

	att := NewAttendeeCheckRepo(f.pool)
	reg, err := att.IsRegistered(ctx, f.eventID, []byte("attendee-1"))
	require.NoError(t, err)
	assert.True(t, reg, "NIK pemegang sudah terdaftar di event ini")
	reg, err = att.IsRegistered(ctx, f.eventID, []byte("orang-lain"))
	require.NoError(t, err)
	assert.False(t, reg)

	rec, err := att.RecordCheck(ctx, f.admissionID, []byte("nik-dicek"), 3)
	require.NoError(t, err)
	assert.True(t, rec.Counted)
	assert.Equal(t, 1, rec.Used)
	rec, err = att.RecordCheck(ctx, f.admissionID, []byte("nik-dicek"), 3)
	require.NoError(t, err)
	assert.False(t, rec.Counted, "NIK yang sama dalam sesi tidak dihitung dua kali")
	assert.Equal(t, 1, rec.Used)
}

// Ten distinct NIKs race for a limit of three: the admission counter and the dedupe rows must stop at exactly three.
func TestRecordCheck_ConcurrentNeverExceedsMax(t *testing.T) {
	f := seed(t)
	ctx := context.Background()
	att := NewAttendeeCheckRepo(f.pool)
	const maxChecks, attempts = 3, 10

	var wg sync.WaitGroup
	recs := make(chan outbound.AttendeeCheckRecord, attempts)
	errs := make(chan error, attempts)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rec, err := att.RecordCheck(ctx, f.admissionID, []byte{'c', byte('0' + i)}, maxChecks)
			if err != nil {
				errs <- err
				return
			}
			recs <- rec
		}(i)
	}
	wg.Wait()
	close(recs)
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	counted, limited := 0, 0
	for rec := range recs {
		switch {
		case rec.Counted:
			counted++
		case rec.LimitReached:
			limited++
			assert.Equal(t, maxChecks, rec.Used)
		}
	}
	assert.Equal(t, maxChecks, counted)
	assert.Equal(t, attempts-maxChecks, limited)
	assert.Equal(t, maxChecks, f.n(t, `SELECT attendee_checks FROM booking.admissions WHERE id = $1::uuid`, f.admissionID))
	assert.Equal(t, maxChecks, f.n(t, `SELECT count(*)::int FROM booking.attendee_checks WHERE admission_id = $1::uuid`, f.admissionID))
}

func TestEventWriteRepo_CreateAndUpdateConfig(t *testing.T) {
	f := seed(t)
	ctx := context.Background()
	w := NewEventWriteRepo(f.pool)
	id := f.q(t, `SELECT gen_random_uuid()::text`)
	req := dto.CreateEventRequest{
		Name: "Festival", Slug: "festival", Venue: "JIS",
		StartsAt: time.Now().Add(72 * time.Hour), SalesStartsAt: time.Now().Add(time.Hour), SalesEndsAt: time.Now().Add(48 * time.Hour),
		MaxTicketsPerOrder: 2, MaxTicketsPerBuyerNIK: 2,
		Categories: []dto.CreateCategoryRequest{{Code: "GA", Name: "General", PriceMinor: 50000, Capacity: 20}},
	}
	require.NoError(t, w.CreateEvent(ctx, id, req))
	d, err := NewEventRepo(f.pool).GetDetail(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, 2, d.Event.MaxTicketsPerOrder)
	assert.Equal(t, 20, d.Categories[0].Available)

	dup := w.CreateEvent(ctx, f.q(t, `SELECT gen_random_uuid()::text`), req)
	require.Error(t, dup, "slug sama ditolak sebagai validasi")

	sel, pay := 120, 900
	require.NoError(t, w.UpdateConfig(ctx, id, dto.UpdateEventConfigRequest{SelectionTTLSeconds: &sel, PaymentTTLSeconds: &pay}))
	e, err := NewEventRepo(f.pool).GetByID(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, 120, e.SelectionTTLSeconds)
	assert.Equal(t, 900, e.PaymentTTLSeconds)
	assert.ErrorIs(t, w.UpdateConfig(ctx, "00000000-0000-0000-0000-000000000000", dto.UpdateEventConfigRequest{SelectionTTLSeconds: &sel}), inbound.ErrNotFound)
}

func TestEventWriteRepo_Description(t *testing.T) {
	f := seed(t)
	ctx := context.Background()
	w, r := NewEventWriteRepo(f.pool), NewEventRepo(f.pool)
	id := f.q(t, `SELECT gen_random_uuid()::text`)
	doc := json.RawMessage(`[{"type":"paragraph","children":[{"text":"halo"}]}]`)
	require.NoError(t, w.CreateEvent(ctx, id, dto.CreateEventRequest{
		Name: "Desc", Slug: "desc", Venue: "JIS", Description: doc,
		StartsAt: time.Now().Add(72 * time.Hour), SalesStartsAt: time.Now().Add(time.Hour), SalesEndsAt: time.Now().Add(48 * time.Hour),
		MaxTicketsPerOrder: 2, MaxTicketsPerBuyerNIK: 2,
		Categories: []dto.CreateCategoryRequest{{Code: "GA", Name: "General", PriceMinor: 50000, Capacity: 20}},
	}))
	e, err := r.GetByID(ctx, id)
	require.NoError(t, err)
	assert.JSONEq(t, string(doc), string(e.Description), "disimpan saat create dan dibaca di detail")

	// Absent leaves the document unchanged (no-op update).
	sel := 100
	require.NoError(t, w.UpdateConfig(ctx, id, dto.UpdateEventConfigRequest{SelectionTTLSeconds: &sel}))
	e, err = r.GetByID(ctx, id)
	require.NoError(t, err)
	assert.JSONEq(t, string(doc), string(e.Description), "tanpa field description tidak berubah")

	// Replace.
	next := json.RawMessage(`[{"type":"h1","children":[{"text":"baru"}]}]`)
	require.NoError(t, w.UpdateConfig(ctx, id, dto.UpdateEventConfigRequest{Description: next}))
	e, err = r.GetByID(ctx, id)
	require.NoError(t, err)
	assert.JSONEq(t, string(next), string(e.Description))

	// JSON null clears it.
	require.NoError(t, w.UpdateConfig(ctx, id, dto.UpdateEventConfigRequest{Description: json.RawMessage(`null`)}))
	e, err = r.GetByID(ctx, id)
	require.NoError(t, err)
	assert.Empty(t, e.Description, "null menghapus deskripsi")

	// The catalog list never loads it.
	list, err := r.ListEvents(ctx, dto.EventListQuery{})
	require.NoError(t, err)
	for _, ev := range list {
		assert.Empty(t, ev.Description)
	}
}

func TestEventRepo_OnSaleAvailability_ClampsAndHonoursTheSalesWindow(t *testing.T) {
	f := seed(t)
	ctx := context.Background()
	repo := NewEventRepo(f.pool)
	now := time.Now()

	got, err := repo.OnSaleAvailability(ctx, now)
	require.NoError(t, err)
	require.Len(t, got[f.eventID], 1)
	assert.Equal(t, 10, got[f.eventID][0].Available)

	_, err = f.pool.Exec(ctx, `UPDATE booking.category_inventories SET held_quantity = 4, sold_quantity = 3 WHERE event_id=$1::uuid`, f.eventID)
	require.NoError(t, err)
	got, err = repo.OnSaleAvailability(ctx, now)
	require.NoError(t, err)
	assert.Equal(t, 3, got[f.eventID][0].Available, "kapasitas - ditahan - terjual")

	got, err = repo.OnSaleAvailability(ctx, now.Add(-72*time.Hour))
	require.NoError(t, err)
	assert.Empty(t, got, "di luar masa jual")

	_, err = f.pool.Exec(ctx, `UPDATE booking.events SET status='CLOSED' WHERE id=$1::uuid`, f.eventID)
	require.NoError(t, err)
	got, err = repo.OnSaleAvailability(ctx, now)
	require.NoError(t, err)
	assert.Empty(t, got, "event CLOSED tidak diproyeksikan")
}

func TestEventWriteRepo_SetImageURL(t *testing.T) {
	f := seed(t)
	ctx := context.Background()
	w := NewEventWriteRepo(f.pool)
	for kind, url := range map[string]string{"image": "https://cdn.test/i.png", "banner": "https://cdn.test/b.png", "thumbnail": "https://cdn.test/t.png"} {
		require.NoError(t, w.SetImageURL(ctx, f.eventID, kind, url))
	}
	e, err := NewEventRepo(f.pool).GetByID(ctx, f.eventID)
	require.NoError(t, err)
	assert.Equal(t, "https://cdn.test/i.png", e.ImageURL)
	assert.Equal(t, "https://cdn.test/b.png", e.BannerURL)
	assert.Equal(t, "https://cdn.test/t.png", e.ThumbnailURL)

	assert.ErrorIs(t, w.SetImageURL(ctx, "00000000-0000-0000-0000-000000000000", "image", "u"), inbound.ErrNotFound)
	assert.ErrorIs(t, w.SetImageURL(ctx, "bukan-uuid", "image", "u"), inbound.ErrNotFound)
	assert.Error(t, w.SetImageURL(ctx, f.eventID, "poster", "u"), "kind di luar daftar")
}

// SCRUM-55: a draft (published_at empty) is not in the catalog, cannot be reserved, and is visible again once published.
func TestEventRepo_DraftIsHiddenAndCannotBeReservedUntilPublished(t *testing.T) {
	f := seed(t)
	ctx := context.Background()
	events := NewEventRepo(f.pool)
	write := NewEventWriteRepo(f.pool)
	admissions := NewAdmissionRepo(f.pool)
	_, err := f.pool.Exec(ctx, `DELETE FROM booking.admissions`)
	require.NoError(t, err)

	listed := func() int {
		got, err := events.ListEvents(ctx, dto.EventListQuery{Status: "ALL"})
		require.NoError(t, err)
		return len(got)
	}
	reserve := func() string {
		out, err := admissions.ReserveBatch(ctx, f.eventID, []outbound.ReserveEntry{{
			QueueEntryID: f.q(t, `SELECT gen_random_uuid()::text`), Seq: 1, VisitorID: f.q(t, `SELECT gen_random_uuid()::text`),
			BuyerNIKHash: []byte{9}, BuyerNIKMasked: "3174********0001", BuyerEmailEnc: sealedEmail, Units: 1,
			AdmissionJTI: f.q(t, `SELECT gen_random_uuid()::text`),
		}}, time.Now())
		require.NoError(t, err)
		return out[0].Outcome
	}

	require.NoError(t, write.SetPublished(ctx, f.eventID, false))
	assert.Equal(t, 0, listed(), "draf tidak masuk katalog")
	detail, err := events.GetDetail(ctx, f.eventID)
	require.NoError(t, err)
	assert.False(t, detail.Event.Published, "repo tetap bisa membaca draf (dipakai admin)")
	assert.Equal(t, "NO_SUPPLY_NOW", reserve(), "draf tidak boleh dialokasikan")

	require.NoError(t, write.SetPublished(ctx, f.eventID, true))
	assert.Equal(t, 1, listed())
	assert.Equal(t, "GRANTED", reserve(), "terbit: alokasi normal")

	var first time.Time
	require.NoError(t, f.pool.QueryRow(ctx, `SELECT published_at FROM booking.events WHERE id=$1::uuid`, f.eventID).Scan(&first))
	require.NoError(t, write.SetPublished(ctx, f.eventID, true))
	var second time.Time
	require.NoError(t, f.pool.QueryRow(ctx, `SELECT published_at FROM booking.events WHERE id=$1::uuid`, f.eventID).Scan(&second))
	assert.True(t, first.Equal(second), "menerbitkan ulang tidak menggeser waktu terbit pertama")

	assert.ErrorIs(t, write.SetPublished(ctx, "00000000-0000-0000-0000-000000000000", true), inbound.ErrNotFound)
	assert.ErrorIs(t, write.SetPublished(ctx, "bukan-uuid", true), inbound.ErrNotFound)
}

// SCRUM-56: a cancelled event leaves the default list, can be listed by status CANCELLED, stays readable by id, and
// cannot be reserved.
func TestEventRepo_CancelledEventLeavesTheListButStaysReadable(t *testing.T) {
	f := seed(t)
	ctx := context.Background()
	events := NewEventRepo(f.pool)
	write := NewEventWriteRepo(f.pool)
	count := func(status string) int {
		got, err := events.ListEvents(ctx, dto.EventListQuery{Status: status})
		require.NoError(t, err)
		return len(got)
	}
	assert.Equal(t, 1, count("ALL"))

	require.NoError(t, write.Cancel(ctx, f.eventID))
	require.NoError(t, write.Cancel(ctx, f.eventID), "idempoten")
	assert.Equal(t, 0, count("ALL"), "dibatalkan: hilang dari daftar bawaan")
	assert.Equal(t, 0, count("AVAILABLE"))
	assert.Equal(t, 1, count("CANCELLED"), "tetap bisa disaring lewat status")

	detail, err := events.GetDetail(ctx, f.eventID)
	require.NoError(t, err)
	assert.EqualValues(t, "CANCELLED", detail.Event.Status, "detail tetap terbaca dengan status CANCELLED")

	out, err := NewAdmissionRepo(f.pool).ReserveBatch(ctx, f.eventID, []outbound.ReserveEntry{{
		QueueEntryID: f.q(t, `SELECT gen_random_uuid()::text`), Seq: 1, VisitorID: f.q(t, `SELECT gen_random_uuid()::text`),
		BuyerNIKHash: []byte{7}, BuyerNIKMasked: "3174********0001", BuyerEmailEnc: sealedEmail, Units: 1,
		AdmissionJTI: f.q(t, `SELECT gen_random_uuid()::text`),
	}}, time.Now())
	require.NoError(t, err)
	assert.Equal(t, "NO_SUPPLY_NOW", out[0].Outcome, "tidak ada admission baru untuk event dibatalkan")

	assert.ErrorIs(t, write.Cancel(ctx, "00000000-0000-0000-0000-000000000000"), inbound.ErrNotFound)
}

// AC-C4.2 (F-status): SOLD_OUT in the catalog comes from supply (held + sold >= total) inside the sales window, not
// from the stored events.status, which stays untouched.
func TestEventRepo_ListAndFilterDeriveSoldOutFromSupply(t *testing.T) {
	f := seed(t)
	ctx := context.Background()
	repo := NewEventRepo(f.pool)
	count := func(status string) int {
		got, err := repo.ListEvents(ctx, dto.EventListQuery{Status: status})
		require.NoError(t, err)
		return len(got)
	}
	assert.Equal(t, 1, count("AVAILABLE"), "stok masih ada")

	_, err := f.pool.Exec(ctx, `UPDATE booking.event_supply SET allocated_units=0, held_units=6, sold_units=4 WHERE event_id=$1::uuid`, f.eventID)
	require.NoError(t, err)
	_, err = f.pool.Exec(ctx, `UPDATE booking.category_inventories SET held_quantity=6, sold_quantity=4 WHERE event_id=$1::uuid`, f.eventID)
	require.NoError(t, err)

	assert.Equal(t, 1, count("SOLD_OUT"), "held + sold = total dalam masa jual")
	assert.Equal(t, 0, count("AVAILABLE"), "tidak lagi tersedia")
	all, err := repo.ListEvents(ctx, dto.EventListQuery{Status: "ALL"})
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.True(t, all[0].StockSoldOut)

	detail, err := repo.GetDetail(ctx, f.eventID)
	require.NoError(t, err)
	assert.True(t, detail.Event.StockSoldOut)
	assert.Equal(t, "SOLD_OUT", mapper.ToEventDetail(detail, time.Now()).Event.Status, "detail sejalan dengan daftar")

	var stored string
	require.NoError(t, f.pool.QueryRow(ctx, `SELECT status FROM booking.events WHERE id=$1::uuid`, f.eventID).Scan(&stored))
	assert.Equal(t, "AVAILABLE", stored, "events.status tidak diubah oleh turunan stok")
}
