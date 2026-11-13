package postgres

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tanpa baris outbox tidak ada statement yang dijalankan.
func TestEnqueueOutboxMany_Empty(t *testing.T) {
	tx := &fakeTx{}
	require.NoError(t, enqueueOutboxMany(context.Background(), tx, nil))
	assert.Empty(t, tx.execs)
}

// Beberapa baris ditulis dalam satu INSERT multi-baris, placeholder berurutan, payload JSON sesuai urutan slice.
func TestEnqueueOutboxMany_MultiRow(t *testing.T) {
	tx := &fakeTx{}
	rows := []outboxRow{
		{"booking.review", "b-1", map[string]any{"bookingId": "b-1"}},
		journeyEndedRow("b-1", "e-1", "q-1", "EXPIRED"),
	}
	require.NoError(t, enqueueOutboxMany(context.Background(), tx, rows))
	require.Len(t, tx.execs, 1)
	assert.Contains(t, tx.execs[0].sql, "($1, $2::uuid, 1, $3), ($4, $5::uuid, 1, $6)")
	args := tx.execs[0].args
	require.Len(t, args, 6)
	assert.Equal(t, []any{"booking.review", "b-1"}, args[0:2])
	assert.JSONEq(t, `{"bookingId":"b-1"}`, string(args[2].([]byte)))
	assert.Equal(t, []any{"booking.journey.ended", "b-1"}, args[3:5])
	assert.JSONEq(t, `{"eventId":"e-1","queueEntryId":"q-1","outcome":"EXPIRED"}`, string(args[5].([]byte)))
}

// Payload yang tidak bisa di-marshal gagal sebelum menyentuh database; error Exec diteruskan.
func TestEnqueueOutboxMany_Errors(t *testing.T) {
	tx := &fakeTx{}
	err := enqueueOutboxMany(context.Background(), tx, []outboxRow{{"x", "b-1", make(chan int)}})
	require.Error(t, err)
	assert.Empty(t, tx.execs)

	tx = &fakeTx{execErr: errDB}
	assert.ErrorIs(t, enqueueOutboxMany(context.Background(), tx, []outboxRow{reviewRow("b-1", "enc", 1)}), errDB)
}

// Kontrak payload booking.review dan booking.journey.ended (docs/contracts/events).
func TestOutboxRowBuilders(t *testing.T) {
	assert.Equal(t, outboxRow{"booking.review", "b-1",
		map[string]any{"bookingId": "b-1", "buyerEmailEnc": "enc", "amountMinor": int64(150000)}},
		reviewRow("b-1", "enc", 150000))
	assert.Equal(t, outboxRow{"booking.journey.ended", "b-1",
		map[string]any{"eventId": "e-1", "queueEntryId": "q-1", "outcome": "PAID"}},
		journeyEndedRow("b-1", "e-1", "q-1", "PAID"))
}
