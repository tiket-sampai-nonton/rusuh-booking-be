//go:build integration

package postgres

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

func fakeSeal(s string) (string, error) { return "sealed:" + strings.ToUpper(s), nil }

// A booking made before PDP-1 only has the plaintext column. It must still settle, and the event must carry ciphertext only.
func TestLegacyPlaintextEmail_IsSealedInEventsAndNeverLeaksOut(t *testing.T) {
	f := seed(t)
	ctx := context.Background()
	f.uow.WithEmailSealer(fakeSeal)
	_, err := f.pool.Exec(ctx, `UPDATE booking.admissions SET buyer_email_enc = NULL, buyer_email = 'pembeli@contoh.id' WHERE id=$1::uuid`, f.admissionID)
	require.NoError(t, err)

	id := f.q(t, `SELECT gen_random_uuid()::text`)
	_, err = f.uow.Hold(ctx, outbound.HoldSpec{
		BookingID: id, PublicRef: "RB-7F3A9C23", EventID: f.eventID, AdmissionID: f.admissionID, VisitorID: visitor,
		BuyerNIKHash: []byte(f.nikH), BuyerEmail: "pembeli@contoh.id", AmountMinor: 150000, CfgEditEnabled: true,
		PaymentExpiresAt: time.Now().Add(10 * time.Minute), CorrelationID: id,
		Lines: []outbound.HoldLine{{CategoryID: f.categoryID, UnitPriceMinor: 150000, AttendeeNIKHash: []byte("a1"), AttendeeNIKEnc: []byte("e"), AttendeeNIKMasked: "3174********0001", AttendeeName: "Budi"}},
	})
	require.NoError(t, err)
	assert.Equal(t, "sealed:PEMBELI@CONTOH.ID", f.q(t, `SELECT buyer_email_enc FROM booking.bookings WHERE id=$1::uuid`, id), "baru ditulis sebagai ciphertext")
	assert.Equal(t, 1, f.n(t, `SELECT count(*) FROM booking.bookings WHERE id=$1::uuid AND buyer_email IS NULL`, id), "kolom plaintext tidak ditulis")

	_, err = f.uow.Settle(ctx, settleSpec(id))
	require.NoError(t, err)
	raw := string(f.outboxPayload(t, "booking.paid"))
	assert.Contains(t, raw, "sealed:PEMBELI@CONTOH.ID")
	assert.NotContains(t, raw, "pembeli@contoh.id")
	assert.NotContains(t, raw, `"buyerEmail"`)
}

func TestBackfillBuyerEmail_SealsLegacyRowsOnceAndClearsPlaintext(t *testing.T) {
	f := seed(t)
	ctx := context.Background()
	id := f.hold(t)
	_, err := f.pool.Exec(ctx, `UPDATE booking.bookings SET buyer_email = 'lama@contoh.id', buyer_email_enc = NULL WHERE id=$1::uuid`, id)
	require.NoError(t, err)
	_, err = f.pool.Exec(ctx, `UPDATE booking.admissions SET buyer_email = 'lama@contoh.id', buyer_email_enc = NULL WHERE id=$1::uuid`, f.admissionID)
	require.NoError(t, err)

	a, b, err := BackfillBuyerEmail(ctx, f.pool, fakeSeal)
	require.NoError(t, err)
	assert.Equal(t, 1, a)
	assert.Equal(t, 1, b)
	assert.Equal(t, "sealed:LAMA@CONTOH.ID", f.q(t, `SELECT buyer_email_enc FROM booking.bookings WHERE id=$1::uuid`, id))
	assert.Equal(t, 0, f.n(t, `SELECT count(*) FROM booking.bookings WHERE buyer_email IS NOT NULL`))
	assert.Equal(t, 0, f.n(t, `SELECT count(*) FROM booking.admissions WHERE buyer_email IS NOT NULL`))

	a, b, err = BackfillBuyerEmail(ctx, f.pool, fakeSeal)
	require.NoError(t, err)
	assert.Equal(t, [2]int{0, 0}, [2]int{a, b}, "idempoten")
}
