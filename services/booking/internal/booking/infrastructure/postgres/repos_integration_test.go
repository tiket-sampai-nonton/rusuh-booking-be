//go:build integration

package postgres

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
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
	byJTI, err := repo.FindByJTI(ctx, jti)
	require.NoError(t, err)
	assert.Equal(t, f.admissionID, byJTI.ID)
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

	released, err := repo.Release(ctx, out[1].AllocationID)
	require.NoError(t, err)
	assert.True(t, released)
	assert.Equal(t, 9, f.n(t, `SELECT allocated_units FROM booking.event_supply WHERE event_id=$1::uuid`, f.eventID))
	released, err = repo.Release(ctx, out[1].AllocationID)
	require.NoError(t, err)
	assert.False(t, released, "melepas dua kali tidak mengubah apa pun")
}

func TestOutboxWriter_PendingFailPublish(t *testing.T) {
	f := seed(t)
	ctx := context.Background()
	id := f.hold(t)
	_, err := f.uow.Settle(ctx, settleSpec(id))
	require.NoError(t, err)
	w := NewOutboxWriter(f.pool)

	rows, err := w.Pending(ctx, 50)
	require.NoError(t, err)
	require.NotEmpty(t, rows)
	first := rows[0]

	require.NoError(t, w.MarkFailed(ctx, first.ID, 1, strings.Repeat("x", 600), time.Minute))
	rows, err = w.Pending(ctx, 50)
	require.NoError(t, err)
	for _, r := range rows {
		assert.NotEqual(t, first.ID, r.ID, "baris gagal ditunda sampai backoff lewat")
	}
	var lastErr string
	require.NoError(t, f.pool.QueryRow(ctx, `SELECT last_error FROM booking.outbox_events WHERE id=$1::uuid`, first.ID).Scan(&lastErr))
	assert.Len(t, lastErr, 512, "pesan error dipotong")

	_, err = f.pool.Exec(ctx, `UPDATE booking.outbox_events SET available_at=now() WHERE id=$1::uuid`, first.ID)
	require.NoError(t, err)
	require.NoError(t, w.MarkPublished(ctx, first.ID))
	assert.Equal(t, 0, f.n(t, `SELECT count(*) FROM booking.outbox_events WHERE id=$1::uuid AND published_at IS NULL`, first.ID))
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

	counted, err := att.RecordCheck(ctx, f.admissionID, []byte("nik-dicek"))
	require.NoError(t, err)
	assert.True(t, counted)
	counted, err = att.RecordCheck(ctx, f.admissionID, []byte("nik-dicek"))
	require.NoError(t, err)
	assert.False(t, counted, "NIK yang sama dalam sesi tidak dihitung dua kali")
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
