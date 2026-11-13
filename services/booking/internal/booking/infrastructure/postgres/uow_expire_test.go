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

// expireTx answers expire for a booking of 2 HELD c-1 units at version 2; started is payment_started_at.
func expireTx(admStatus, status string, started any) *fakeTx {
	return (&fakeTx{}).
		row("e-1", "a-1").
		row(admStatus).
		row(status, 2, "R-1", "enc", []byte("h"), int64(200), started)
}

// expireTxFull continues expireTx through the HELD items, the queue entry, the counter move and the admission.
func expireTxFull(admStatus string) *fakeTx {
	tx := expireTx(admStatus, "PENDING_PAYMENT", nil).
		query([]any{"c-1", 2}).
		row("q-1").
		row(0, 0, 0, 10, 0, 3)
	if admStatus == "EDITABLE" {
		tx.row(0)
	}
	return tx
}

// Booking yang sudah terminal tidak disentuh lagi; cancel setelah payment-lock ditolak sebelum stok ditulis (P1-4).
func TestExpire_Guards(t *testing.T) {
	u, err := NewUnitOfWork(nil)
	require.NoError(t, err)

	tx := expireTx("CONVERTED", "PAID", nil)
	acted, err := u.expire(tctx, tx, outbound.ExpireSpec{BookingID: "b-1", Reason: "PAYMENT_TIMEOUT"})
	require.NoError(t, err)
	assert.False(t, acted)
	assert.Empty(t, tx.execs)

	tx = expireTx("CONVERTED", "PENDING_PAYMENT", time.Now())
	_, err = u.expire(tctx, tx, outbound.ExpireSpec{BookingID: "b-1", Reason: "CANCELLED"})
	assert.ErrorIs(t, err, inbound.ErrBookingLocked)
	assert.Empty(t, tx.execs)

	_, err = u.expire(tctx, (&fakeTx{}).rowErr(pgx.ErrNoRows), outbound.ExpireSpec{BookingID: "b-1"})
	assert.ErrorIs(t, err, inbound.ErrNotFound)
}

// Tiga jalur akhir: EXPIRED dan CANCELLED (booking.expired) atau REVIEW (booking.review). Semua melepas stok held,
// menutup admission EDITABLE, dan mengirim journey.ended dengan outcome yang sesuai.
func TestExpire_Paths(t *testing.T) {
	u, err := NewUnitOfWork(nil)
	require.NoError(t, err)
	tests := []struct {
		name        string
		spec        outbound.ExpireSpec
		admStatus   string
		wantEvent   string
		wantStatus  string // the bookings UPDATE that must exist
		wantOutbox  []string
		wantOutcome string
	}{
		{"kedaluwarsa", outbound.ExpireSpec{BookingID: "b-1", Reason: "PAYMENT_TIMEOUT"}, "EDITABLE",
			"BookingExpired@3", "status=$3, ended_at=now()", []string{"booking.expired", "booking.journey.ended"}, "EXPIRED"},
		{"dibatalkan", outbound.ExpireSpec{BookingID: "b-1", Reason: "CANCELLED"}, "CONVERTED",
			"BookingExpired@3", "status=$3, ended_at=now()", []string{"booking.expired", "booking.journey.ended"}, "CANCELLED"},
		{"review", outbound.ExpireSpec{BookingID: "b-1", Reason: "PAYMENT_TIMEOUT", Review: true}, "EDITABLE",
			"BookingReview@3", "status='REVIEW'", []string{"booking.review", "booking.journey.ended"}, "EXPIRED"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx := expireTxFull(tt.admStatus)
			acted, err := u.expire(tctx, tx, tt.spec)
			require.NoError(t, err)
			assert.True(t, acted)
			assert.Equal(t, []string{tt.wantEvent}, tx.events())
			require.NotNil(t, tx.exec(tt.wantStatus))
			require.NotNil(t, tx.exec("item_status='RELEASED'"))
			assert.Equal(t, tt.wantOutbox, tx.outbox())
			assert.Equal(t, tt.wantOutcome, tx.outboxPayload(t, "booking.journey.ended")["outcome"])
			if tt.admStatus == "EDITABLE" {
				assert.NotNil(t, tx.exec("lock_reason='BOOKING_ENDED'"))
			} else {
				assert.Nil(t, tx.exec("UPDATE booking.admissions"))
			}
		})
	}
}

// Status akhir booking mengikuti alasan: CANCELLED untuk pembatalan, EXPIRED untuk yang lain; payload booking.expired
// membawa publicRef dan alasan.
func TestExpire_FinalStatusAndPayload(t *testing.T) {
	u, err := NewUnitOfWork(nil)
	require.NoError(t, err)
	for reason, want := range map[string]string{"CANCELLED": "CANCELLED", "SESSION_EXPIRED": "EXPIRED"} {
		tx := expireTxFull("CONVERTED")
		_, err := u.expire(tctx, tx, outbound.ExpireSpec{BookingID: "b-1", Reason: reason})
		require.NoError(t, err)
		assert.Equal(t, []any{"b-1", 3, want}, tx.exec("status=$3").args)
		assert.Equal(t, map[string]any{"bookingId": "b-1", "publicRef": "R-1", "buyerEmailEnc": "enc", "reason": reason},
			tx.outboxPayload(t, "booking.expired"))
	}
}

func TestExpire_Errors(t *testing.T) {
	u, err := NewUnitOfWork(nil)
	require.NoError(t, err)
	tx := expireTxFull("CONVERTED")
	tx.execErr, tx.execErrOn = errDB, "item_status='RELEASED'"
	_, err = u.expire(tctx, tx, outbound.ExpireSpec{BookingID: "b-1", Reason: "PAYMENT_TIMEOUT"})
	assert.ErrorIs(t, err, errDB)
	assert.Empty(t, tx.outbox(), "nothing published when the release fails")
}
