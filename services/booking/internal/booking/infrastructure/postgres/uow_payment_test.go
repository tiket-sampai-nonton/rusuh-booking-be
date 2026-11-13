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

var payNow = time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)

// paymentLockTx answers the booking refs, the admission (status, outstanding units) and the booking row
// (status, version, amount, payment_expires_at, payment_started_at).
func paymentLockTx(admStatus string, admUnits int, status string, expiresAt time.Time, started any) *fakeTx {
	return (&fakeTx{}).
		row("e-1", "a-1").
		row(admStatus, admUnits).
		row(status, 2, int64(150000), expiresAt, started)
}

func TestPaymentLock_Outcomes(t *testing.T) {
	u, err := NewUnitOfWork(nil)
	require.NoError(t, err)
	spec := outbound.PaymentLockSpec{BookingID: "b-1", Now: payNow, CorrelationID: "c-1"}
	live := payNow.Add(5 * time.Minute)

	tests := []struct {
		name string
		tx   *fakeTx
		want outbound.PaymentLockResult
	}{
		{"jendela bayar lewat (apa pun statusnya)", paymentLockTx("CONVERTED", 0, "PENDING_PAYMENT", payNow.Add(-time.Second), nil),
			outbound.PaymentLockResult{Outcome: "PAYMENT_EXPIRED", AmountMinor: 150000, PaymentExpiresAt: payNow.Add(-time.Second)}},
		{"booking tidak payable", paymentLockTx("CONVERTED", 0, "PAID", live, nil),
			outbound.PaymentLockResult{Outcome: "BOOKING_NOT_PAYABLE", AmountMinor: 150000, PaymentExpiresAt: live}},
		{"sudah dikunci sebelumnya (idempoten)", paymentLockTx("CONVERTED", 0, "PENDING_PAYMENT", live, payNow.Add(-time.Minute)),
			outbound.PaymentLockResult{Outcome: "OK", Version: 2, AmountMinor: 150000, PaymentExpiresAt: live}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := u.paymentLock(tctx, tt.tx, spec)
			require.NoError(t, err)
			assert.Equal(t, tt.want, res)
			assert.Empty(t, tt.tx.execs, "no write")
		})
	}
}

// Kunci pembayaran pertama: payment_started_at dicap jam aplikasi, admission EDITABLE dikonversi (PAYMENT_STARTED),
// unit sisa baris lama dikembalikan, dan event BookingLocked ditulis. VERIFYING tanpa cap juga dikunci (P1-5).
func TestPaymentLock_Locks(t *testing.T) {
	u, err := NewUnitOfWork(nil)
	require.NoError(t, err)
	live := payNow.Add(5 * time.Minute)
	spec := outbound.PaymentLockSpec{BookingID: "b-1", Now: payNow, CorrelationID: "c-1"}

	tx := paymentLockTx("EDITABLE", 1, "PENDING_PAYMENT", live, nil)
	res, err := u.paymentLock(tctx, tx, spec)
	require.NoError(t, err)
	assert.Equal(t, outbound.PaymentLockResult{Outcome: "OK", Version: 3, AmountMinor: 150000, PaymentExpiresAt: live}, res)
	assert.Equal(t, []any{"b-1", payNow, 3}, tx.exec("payment_started_at = COALESCE").args)
	assert.Equal(t, []any{"e-1", 1}, tx.exec("UPDATE booking.event_supply").args)
	require.NotNil(t, tx.exec("lock_reason = 'PAYMENT_STARTED'"))
	assert.Equal(t, []string{"BookingLocked@3"}, tx.events())

	tx = paymentLockTx("CONVERTED", 0, "VERIFYING", live, nil)
	_, err = u.paymentLock(tctx, tx, spec)
	require.NoError(t, err)
	assert.Nil(t, tx.exec("UPDATE booking.admissions"), "admission already converted")
	assert.Nil(t, tx.exec("UPDATE booking.event_supply"))
	assert.Equal(t, []string{"BookingLocked@3"}, tx.events())
}

func TestPaymentLock_Errors(t *testing.T) {
	u, err := NewUnitOfWork(nil)
	require.NoError(t, err)
	_, err = u.paymentLock(tctx, (&fakeTx{}).rowErr(pgx.ErrNoRows), outbound.PaymentLockSpec{BookingID: "b-1"})
	assert.ErrorIs(t, err, inbound.ErrNotFound)

	tx := paymentLockTx("ACTIVE", 0, "PENDING_PAYMENT", payNow.Add(time.Minute), nil)
	tx.execErr = errDB
	_, err = u.paymentLock(tctx, tx, outbound.PaymentLockSpec{BookingID: "b-1", Now: payNow})
	assert.ErrorIs(t, err, errDB)
}

// Perpanjangan tenggat sekali per booking, hanya saat PENDING_PAYMENT dan sebelum tenggat (D-B3).
func TestExtendDeadline(t *testing.T) {
	u, err := NewUnitOfWork(nil)
	require.NoError(t, err)
	spec := outbound.DeadlineSpec{BookingID: "b-1", Reason: "GATEWAY_SLOW", Seconds: 120}
	future := time.Now().UTC().Add(time.Hour)

	tests := []struct {
		name    string
		tx      *fakeTx
		wantErr error
	}{
		{"booking tidak ada", (&fakeTx{}).rowErr(pgx.ErrNoRows), inbound.ErrNotFound},
		{"error database", (&fakeTx{}).rowErr(errDB), errDB},
		{"sudah pernah diperpanjang", (&fakeTx{}).row("PENDING_PAYMENT", 1, future, time.Now()), inbound.ErrDeadlineAlreadyExtended},
		{"bukan PENDING_PAYMENT", (&fakeTx{}).row("VERIFYING", 1, future, nil), inbound.ErrNotPayable},
		{"tenggat sudah lewat", (&fakeTx{}).row("PENDING_PAYMENT", 1, time.Now().UTC().Add(-time.Second), nil), inbound.ErrPaymentExpired},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.ErrorIs(t, u.extendDeadline(tctx, tt.tx, spec), tt.wantErr)
			assert.Empty(t, tt.tx.execs)
		})
	}

	tx := (&fakeTx{}).row("PENDING_PAYMENT", 1, future, nil)
	require.NoError(t, u.extendDeadline(tctx, tx, spec))
	assert.Equal(t, []any{"b-1", 120}, tx.exec("make_interval(secs => $2)").args)
	assert.Equal(t, []string{"BookingDeadlineExtended@2"}, tx.events())
}

// Fase A sweeper: hanya PENDING_PAYMENT yang lewat tenggat yang menjadi VERIFYING; selain itu no-op idempoten.
func TestStartVerifying(t *testing.T) {
	u, err := NewUnitOfWork(nil)
	require.NoError(t, err)
	past := time.Now().UTC().Add(-time.Second)

	tests := []struct {
		name      string
		tx        *fakeTx
		wantActed bool
		wantErr   error
	}{
		{"booking tidak ada", (&fakeTx{}).rowErr(pgx.ErrNoRows), false, inbound.ErrNotFound},
		{"error database", (&fakeTx{}).rowErr(errDB), false, errDB},
		{"sudah VERIFYING", (&fakeTx{}).row("VERIFYING", 2, past), false, nil},
		{"belum lewat tenggat", (&fakeTx{}).row("PENDING_PAYMENT", 2, time.Now().UTC().Add(time.Hour)), false, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			acted, err := u.startVerifying(tctx, tt.tx, "b-1", 180)
			assert.Equal(t, tt.wantActed, acted)
			if tt.wantErr == nil {
				assert.NoError(t, err)
			} else {
				assert.ErrorIs(t, err, tt.wantErr)
			}
			assert.Empty(t, tt.tx.execs)
		})
	}

	tx := (&fakeTx{}).row("PENDING_PAYMENT", 2, past)
	before := time.Now().UTC()
	acted, err := u.startVerifying(tctx, tx, "b-1", 180)
	require.NoError(t, err)
	assert.True(t, acted)
	assert.Equal(t, []string{"PaymentVerifying@3"}, tx.events())
	upd := tx.exec("status='VERIFYING'")
	require.NotNil(t, upd)
	assert.Equal(t, 3, upd.args[1])
	until := upd.args[2].(time.Time)
	assert.WithinDuration(t, before.Add(180*time.Second), until, 5*time.Second)
}
