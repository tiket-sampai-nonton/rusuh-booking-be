//go:build integration

package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

func TestExtendDeadline_OncePerBooking(t *testing.T) {
	f := seed(t)
	ctx := context.Background()
	id := f.hold(t)
	var before time.Time
	require.NoError(t, f.pool.QueryRow(ctx, `SELECT payment_expires_at FROM booking.bookings WHERE id=$1::uuid`, id).Scan(&before))

	require.NoError(t, f.uow.ExtendDeadline(ctx, outbound.DeadlineSpec{BookingID: id, Reason: "PAYMENT_PENDING", Seconds: 120, CorrelationID: id}))
	var after time.Time
	require.NoError(t, f.pool.QueryRow(ctx, `SELECT payment_expires_at FROM booking.bookings WHERE id=$1::uuid`, id).Scan(&after))
	assert.Equal(t, 120*time.Second, after.Sub(before))

	err := f.uow.ExtendDeadline(ctx, outbound.DeadlineSpec{BookingID: id, Reason: "PAYMENT_PENDING", Seconds: 120, CorrelationID: id})
	assert.ErrorIs(t, err, inbound.ErrDeadlineAlreadyExtended, "perpanjangan hanya sekali")
}

func TestStartVerifying_OnlyWhenPastTheDeadline(t *testing.T) {
	f := seed(t)
	ctx := context.Background()
	id := f.hold(t)

	acted, err := f.uow.StartVerifying(ctx, id, 180)
	require.NoError(t, err)
	assert.False(t, acted, "belum lewat batas bayar")

	_, err = f.pool.Exec(ctx, `UPDATE booking.bookings SET created_at = now() - interval '2 hours', payment_expires_at = now() - interval '1 minute' WHERE id=$1::uuid`, id)
	require.NoError(t, err)
	acted, err = f.uow.StartVerifying(ctx, id, 180)
	require.NoError(t, err)
	assert.True(t, acted)
	assert.Equal(t, "VERIFYING", f.status(t, id))
	var until time.Time
	require.NoError(t, f.pool.QueryRow(ctx, `SELECT verification_until FROM booking.bookings WHERE id=$1::uuid`, id).Scan(&until))
	assert.WithinDuration(t, time.Now().Add(180*time.Second), until, 10*time.Second, "masa tenggang verifikasi")

	acted, err = f.uow.StartVerifying(ctx, id, 180)
	require.NoError(t, err)
	assert.False(t, acted, "idempoten")
	_, err = f.uow.StartVerifying(ctx, "00000000-0000-0000-0000-000000000000", 180)
	assert.ErrorIs(t, err, inbound.ErrNotFound)
}

func TestReplaceItems_SwapsTheHeldLinesAndKeepsStockConsistent(t *testing.T) {
	f := seed(t)
	ctx := context.Background()
	id := f.hold(t)
	_, err := f.pool.Exec(ctx, `UPDATE booking.event_supply SET allocated_units = 2 WHERE event_id=$1::uuid`, f.eventID) // admission now covers 2 units
	require.NoError(t, err)
	_, err = f.pool.Exec(ctx, `UPDATE booking.admissions SET units = 2, status='EDITABLE' WHERE id=$1::uuid`, f.admissionID)
	require.NoError(t, err)

	line := func(name, nik string) outbound.HoldLine {
		return outbound.HoldLine{
			CategoryID: f.categoryID, UnitPriceMinor: 150000, AttendeeNIKHash: []byte(nik), AttendeeNIKEnc: []byte("enc"),
			AttendeeNIKMasked: "3174********0002", AttendeeName: name,
		}
	}
	version, err := f.uow.ReplaceItems(ctx, outbound.ReplaceSpec{
		BookingID: id, AdmissionID: f.admissionID, EventID: f.eventID, BuyerNIKHash: []byte(f.nikH), ExpectedVersion: 1,
		NewAmountMinor: 300000, Lines: []outbound.HoldLine{line("Siti", "attendee-2"), line("Budi", "attendee-3")}, CorrelationID: id,
	})
	require.NoError(t, err)
	assert.Equal(t, 2, version)
	assert.Equal(t, 2, f.n(t, `SELECT count(*) FROM booking.booking_items WHERE booking_id=$1::uuid AND item_status='HELD'`, id))
	assert.Equal(t, 1, f.n(t, `SELECT count(*) FROM booking.booking_items WHERE booking_id=$1::uuid AND item_status='REPLACED'`, id), "baris lama diganti, tidak dihapus")
	assert.Equal(t, 2, f.n(t, `SELECT held_quantity FROM booking.category_inventories WHERE category_id=$1::uuid`, f.categoryID))
	assert.EqualValues(t, 300000, f.n(t, `SELECT amount_minor FROM booking.bookings WHERE id=$1::uuid`, id))

	// A stale client (old version) is refused without changing anything.
	_, err = f.uow.ReplaceItems(ctx, outbound.ReplaceSpec{
		BookingID: id, AdmissionID: f.admissionID, EventID: f.eventID, BuyerNIKHash: []byte(f.nikH), ExpectedVersion: 1,
		NewAmountMinor: 150000, Lines: []outbound.HoldLine{line("Siti", "attendee-2")}, CorrelationID: id,
	})
	assert.ErrorIs(t, err, inbound.ErrVersionConflict)
	assert.Equal(t, 2, f.n(t, `SELECT count(*) FROM booking.booking_items WHERE booking_id=$1::uuid AND item_status='HELD'`, id))

	// Once payment started the order is locked.
	_, err = f.uow.PaymentLock(ctx, outbound.PaymentLockSpec{BookingID: id, AttemptKey: "a-1", CorrelationID: id})
	require.NoError(t, err)
	_, err = f.uow.ReplaceItems(ctx, outbound.ReplaceSpec{
		BookingID: id, AdmissionID: f.admissionID, EventID: f.eventID, BuyerNIKHash: []byte(f.nikH), ExpectedVersion: 3,
		NewAmountMinor: 150000, Lines: []outbound.HoldLine{line("Siti", "attendee-2")}, CorrelationID: id,
	})
	assert.ErrorIs(t, err, inbound.ErrBookingLocked)
}
