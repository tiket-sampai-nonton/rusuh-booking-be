package postgres

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	inbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

var (
	settleDeadline = time.Date(2026, 1, 1, 10, 10, 0, 0, time.UTC)
	eventStart     = time.Date(2026, 2, 1, 19, 0, 0, 0, time.UTC)
)

func unitSettleSpec() outbound.SettleSpec {
	return outbound.SettleSpec{
		BookingID: "b-1", Method: "QRIS", CorrelationID: "c-1", AmountMinor: 200,
		SettlementTime:    settleDeadline.Add(-time.Minute),
		TicketCodeFn:      func(itemID string) string { return "T-" + itemID },
		TicketLinkTokenFn: func(string) string { return "link-token" },
	}
}

// settleTx answers settle up to the supply lock for a payable booking of 2 HELD c-1 units (version 2, amount 200).
func settleTx(admStatus, status string) *fakeTx {
	return (&fakeTx{}).
		row("e-1", "a-1").
		row(admStatus).
		row(status, 2, []byte("h"), int64(200), "R-1", "enc", settleDeadline).
		row("q-1").
		query([]any{"c-1", 2}).
		row("Konser", "GBK", eventStart)
}

// PAID dan REVIEW adalah no-op idempoten (design §9.5); booking tidak ada → ErrNotFound.
func TestSettle_NoOp(t *testing.T) {
	u, err := NewUnitOfWork(nil)
	require.NoError(t, err)
	for _, status := range []string{"PAID", "REVIEW"} {
		t.Run(status, func(t *testing.T) {
			tx := (&fakeTx{}).row("e-1", "a-1").row("CONVERTED").row(status, 3, []byte("h"), int64(200), "R-1", "enc", settleDeadline)
			settled, err := u.settle(tctx, tx, unitSettleSpec())
			require.NoError(t, err)
			assert.False(t, settled)
			assert.Empty(t, tx.execs)
		})
	}

	_, err = u.settle(tctx, (&fakeTx{}).rowErr(pgx.ErrNoRows), unitSettleSpec())
	assert.ErrorIs(t, err, inbound.ErrNotFound)
}

// Pembayaran untuk booking yang sudah EXPIRED/CANCELLED → REVIEW tanpa perubahan stok (stok sudah dilepas).
func TestSettle_EndedBookingGoesToReview(t *testing.T) {
	u, err := NewUnitOfWork(nil)
	require.NoError(t, err)
	for _, status := range []string{"EXPIRED", "CANCELLED"} {
		t.Run(status, func(t *testing.T) {
			tx := (&fakeTx{}).row("e-1", "a-1").row("CONVERTED").
				row(status, 3, []byte("h"), int64(200), "R-1", "enc", settleDeadline).row("q-1")
			settled, err := u.settle(tctx, tx, unitSettleSpec())
			require.NoError(t, err)
			assert.False(t, settled)
			assert.Equal(t, []string{"BookingReview@4"}, tx.events())
			assert.Equal(t, []string{"booking.review"}, tx.outbox())
			assert.Nil(t, tx.exec("event_supply"), "no supply lock, no stock change")
			assert.Equal(t, map[string]any{"bookingId": "b-1", "buyerEmailEnc": "enc", "amountMinor": float64(200)},
				tx.outboxPayload(t, "booking.review"))
		})
	}
}

// Pembayaran terlambat atau nominal berbeda → REVIEW, stok held dilepas, tidak ada tiket (Property 7, P2-8).
// settlement_time tetap NULL bila gateway tidak memberi waktu.
func TestSettle_LateOrMismatchGoesToReview(t *testing.T) {
	u, err := NewUnitOfWork(nil)
	require.NoError(t, err)
	late := unitSettleSpec()
	late.SettlementTime = settleDeadline.Add(time.Second)
	mismatch := unitSettleSpec()
	mismatch.AmountMinor = 199
	mismatch.SettlementTime = time.Time{}

	tests := []struct {
		name     string
		spec     outbound.SettleSpec
		wantTime any
	}{
		{"terlambat", late, &late.SettlementTime},
		{"nominal berbeda tanpa waktu settlement", mismatch, (*time.Time)(nil)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx := settleTx("CONVERTED", "PENDING_PAYMENT").row(0, 0, 0, 10, nil, nil)
			settled, err := u.settle(tctx, tx, tt.spec)
			require.NoError(t, err)
			assert.False(t, settled)
			assert.Equal(t, []string{"BookingReview@3"}, tx.events())
			require.NotNil(t, tx.exec("item_status='RELEASED'"))
			assert.Equal(t, 0, tx.reads[len(tx.reads)-1].args[3], "held back to free stock, not sold")
			upd := tx.exec("status='REVIEW', settlement_time=$2")
			require.NotNil(t, upd)
			assert.Equal(t, tt.wantTime, upd.args[1])
			assert.Nil(t, tx.exec("booking.tickets"))
			assert.Equal(t, []string{"booking.review", "booking.journey.ended"}, tx.outbox())
			assert.Equal(t, "EXPIRED", tx.outboxPayload(t, "booking.journey.ended")["outcome"])
		})
	}
}

// Pembayaran tepat waktu: PAID, tiket per item, held → sold, admission EDITABLE ditutup, dan outbox lengkap.
// Stok habis tanpa alokasi/hold tersisa → sold_out; kuota NIK penuh → quota.exhausted.
func TestSettle_Paid(t *testing.T) {
	u, err := NewUnitOfWork(nil)
	require.NoError(t, err)
	tx := settleTx("EDITABLE", "PENDING_PAYMENT").
		query([]any{"i-1"}, []any{"i-2"}).
		row(0, 0, 10, 10, 3, 3).
		row(0).
		query([]any{"T-i-1", "Peserta Satu", "32**", "VIP"}, []any{"T-i-2", "Peserta Dua", "33**", "VIP"})

	settled, err := u.settle(tctx, tx, unitSettleSpec())
	require.NoError(t, err)
	assert.True(t, settled)
	assert.Equal(t, []string{"BookingPaid@3"}, tx.events())

	paid := tx.exec("status='PAID'")
	require.NotNil(t, paid)
	assert.Equal(t, settleDeadline.Add(-time.Minute), paid.args[2], "settlement_time from the gateway")
	assert.Equal(t, 3, paid.args[3])

	tickets := tx.exec("INSERT INTO booking.tickets")
	require.NotNil(t, tickets)
	assert.Equal(t, []string{"i-1", "i-2"}, tickets.args[1])
	assert.Equal(t, []string{"T-i-1", "T-i-2"}, tickets.args[2])
	require.NotNil(t, tx.exec("lock_reason='BOOKING_ENDED'"), "EDITABLE admission converted")

	assert.Equal(t, []string{"booking.sold_out", "booking.quota.exhausted", "booking.paid", "booking.journey.ended"}, tx.outbox())
	assert.Equal(t, "68", tx.outboxPayload(t, "booking.quota.exhausted")["buyerNikHash"], "hex of the NIK hash")
	p := tx.outboxPayload(t, "booking.paid")
	assert.Equal(t, "R-1", p["publicRef"])
	assert.Equal(t, "link-token", p["ticketLinkToken"])
	assert.Equal(t, map[string]any{"eventId": "e-1", "name": "Konser", "venue": "GBK", "startsAt": "2026-02-01T19:00:00Z"}, p["event"])
	assert.Len(t, p["tickets"], 2)
	assert.Equal(t, "PAID", tx.outboxPayload(t, "booking.journey.ended")["outcome"])
}

// Stok masih bisa kembali (alokasi lain) dan kuota belum penuh → tanpa sold_out/quota.exhausted; waktu settlement
// tidak diketahui → kolom diisi waktu Booking settle; tanpa TicketLinkTokenFn token kosong.
func TestSettle_PaidWithoutSignals(t *testing.T) {
	u, err := NewUnitOfWork(nil)
	require.NoError(t, err)
	spec := unitSettleSpec()
	spec.SettlementTime = time.Time{}
	spec.TicketLinkTokenFn = nil
	tx := settleTx("CONVERTED", "VERIFYING").
		query([]any{"i-1"}, []any{"i-2"}).
		row(4, 0, 6, 10, 2, 3).
		query()

	before := time.Now().UTC()
	settled, err := u.settle(tctx, tx, spec)
	require.NoError(t, err)
	assert.True(t, settled)
	assert.Equal(t, []string{"booking.paid", "booking.journey.ended"}, tx.outbox())
	assert.Nil(t, tx.exec("UPDATE booking.admissions"))
	paid := tx.exec("status='PAID'")
	assert.False(t, paid.args[2].(time.Time).Before(before))
	assert.Equal(t, "", tx.outboxPayload(t, "booking.paid")["ticketLinkToken"])
}

func TestSettle_Errors(t *testing.T) {
	u, err := NewUnitOfWork(nil)
	require.NoError(t, err)

	tx := settleTx("CONVERTED", "PENDING_PAYMENT")
	tx.execErr, tx.execErrOn = errDB, "FOR UPDATE"
	_, err = u.settle(tctx, tx, unitSettleSpec())
	assert.ErrorIs(t, err, errDB, "supply lock failure")

	tx = settleTx("CONVERTED", "PENDING_PAYMENT").query([]any{"i-1"})
	tx.execErr, tx.execErrOn = errDB, "INSERT INTO booking.tickets"
	_, err = u.settle(tctx, tx, unitSettleSpec())
	assert.ErrorIs(t, err, errDB)
}

// Kegagalan membaca daftar tiket tidak menggagalkan settlement (dipertahankan dari sebelum refactor).
func TestPaidTicketLines_ReadErrorKeepsSettling(t *testing.T) {
	assert.Empty(t, paidTicketLines(tctx, &fakeTx{}, "b-1"))
	lines := paidTicketLines(tctx, (&fakeTx{}).query([]any{"T-1", "Peserta", "32**", "VIP"}), "b-1")
	assert.Equal(t, []ticketLine{{TicketCode: "T-1", AttendeeName: "Peserta", AttendeeNIKMasked: "32**", CategoryCode: "VIP"}}, lines)
}
