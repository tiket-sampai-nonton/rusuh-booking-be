package postgres

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	inbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

var tctx = context.Background()

// Booking yang tidak ada menjadi ErrNotFound; error lain diteruskan apa adanya.
func TestBookingRefs(t *testing.T) {
	ev, adm, err := bookingRefs(tctx, (&fakeTx{}).row("e-1", "a-1"), "b-1")
	require.NoError(t, err)
	assert.Equal(t, "e-1", ev)
	assert.Equal(t, "a-1", adm)

	_, _, err = bookingRefs(tctx, (&fakeTx{}).rowErr(pgx.ErrNoRows), "b-1")
	assert.ErrorIs(t, err, inbound.ErrNotFound)

	_, _, err = bookingRefs(tctx, (&fakeTx{}).rowErr(errDB), "b-1")
	assert.ErrorIs(t, err, errDB)
}

func TestLockStatements(t *testing.T) {
	tx := &fakeTx{}
	require.NoError(t, lockEventSupply(tctx, tx, "e-1"))
	require.NoError(t, lockCategory(tctx, tx, "c-1"))
	require.Len(t, tx.execs, 2)
	assert.Contains(t, tx.execs[0].sql, "event_supply")
	assert.Contains(t, tx.execs[0].sql, "FOR UPDATE")
	assert.Equal(t, []any{"e-1"}, tx.execs[0].args)
	assert.Contains(t, tx.execs[1].sql, "category_inventories")
	assert.Equal(t, []any{"c-1"}, tx.execs[1].args)

	assert.ErrorIs(t, lockEventSupply(tctx, &fakeTx{execErr: errDB}, "e-1"), errDB)
	assert.ErrorIs(t, lockCategory(tctx, &fakeTx{execErr: errDB}, "c-1"), errDB)
}

// Setelah hold ADR-030 counter outstanding = 0, jadi tidak ada stok yang dikembalikan; baris lama (>0) dikembalikan
// ke stok bebas dan counter dinolkan.
func TestReleaseOutstanding(t *testing.T) {
	tx := (&fakeTx{}).row(0)
	require.NoError(t, releaseOutstanding(tctx, tx, "e-1", "a-1"))
	assert.Empty(t, tx.execs)

	tx = (&fakeTx{}).row(2)
	require.NoError(t, releaseOutstanding(tctx, tx, "e-1", "a-1"))
	require.Len(t, tx.execs, 2)
	assert.Contains(t, tx.execs[0].sql, "allocated_units=allocated_units-$2")
	assert.Equal(t, []any{"e-1", 2}, tx.execs[0].args)
	assert.Contains(t, tx.execs[1].sql, "outstanding_units=0")

	assert.ErrorIs(t, releaseOutstanding(tctx, (&fakeTx{}).rowErr(errDB), "e-1", "a-1"), errDB)
	assert.ErrorIs(t, releaseOutstanding(tctx, (&fakeTx{execErr: errDB}).row(2), "e-1", "a-1"), errDB)
}

// Admission EDITABLE ditutup menjadi CONVERTED(BOOKING_ENDED) hanya bila masih EDITABLE (guard di WHERE).
func TestEndEditableAdmission(t *testing.T) {
	tx := (&fakeTx{}).row(0)
	require.NoError(t, endEditableAdmission(tctx, tx, "e-1", "a-1"))
	require.Len(t, tx.execs, 1)
	assert.Contains(t, tx.execs[0].sql, "lock_reason='BOOKING_ENDED'")
	assert.Contains(t, tx.execs[0].sql, "AND status='EDITABLE'")
	assert.Equal(t, []any{"a-1"}, tx.execs[0].args)

	assert.ErrorIs(t, endEditableAdmission(tctx, (&fakeTx{}).rowErr(errDB), "e-1", "a-1"), errDB)
}

func TestHeldByCategory(t *testing.T) {
	tx := &fakeTx{queries: []*txRows{{data: [][]any{{"c-1", 2}, {"c-2", 1}}}}}
	got, err := heldByCategory(tctx, tx, "b-1")
	require.NoError(t, err)
	assert.Equal(t, map[string]int{"c-1": 2, "c-2": 1}, got)

	tx = &fakeTx{queries: []*txRows{{err: errDB}}}
	_, err = heldByCategory(tctx, tx, "b-1")
	assert.ErrorIs(t, err, errDB)

	_, err = heldByCategory(tctx, &fakeTx{}, "b-1")
	assert.Error(t, err)
}

// Booking tanpa item HELD tidak melepas apa pun, jadi panggilan ulang tidak melepas dua kali.
func TestReleaseHeldItems_NothingHeld(t *testing.T) {
	tx := &fakeTx{}
	require.NoError(t, releaseHeldItems(tctx, tx, "b-1", "e-1", []byte("h"), map[string]int{}))
	assert.Empty(t, tx.execs)
	assert.Empty(t, tx.reads)
}

// Item HELD menjadi RELEASED dan counter held turun tanpa menambah sold (soldMul 0).
func TestReleaseHeldItems_Releases(t *testing.T) {
	tx := (&fakeTx{}).row(0, 0, 0, 10, nil, nil)
	require.NoError(t, releaseHeldItems(tctx, tx, "b-1", "e-1", []byte("h"), map[string]int{"c-1": 2}))
	require.Len(t, tx.execs, 1)
	assert.Contains(t, tx.execs[0].sql, "item_status='RELEASED'")
	require.Len(t, tx.reads, 1)
	assert.Equal(t, 2, tx.reads[0].args[2], "n")
	assert.Equal(t, 0, tx.reads[0].args[3], "soldMul")

	assert.ErrorIs(t, releaseHeldItems(tctx, &fakeTx{execErr: errDB}, "b-1", "e-1", nil, map[string]int{"c-1": 1}), errDB)
}

// Settle memindahkan held ke sold (soldMul 1) dan mengembalikan nilai supply dan kuota setelahnya.
func TestMoveHeldCounters(t *testing.T) {
	paid, maxQ := 3, 3
	tx := (&fakeTx{}).row(0, 0, 10, 10, paid, maxQ)
	c, err := moveHeldCounters(tctx, tx, "e-1", []byte("h"), map[string]int{"c-1": 2, "c-2": 1}, true)
	require.NoError(t, err)
	assert.Equal(t, 10, c.sold)
	assert.Equal(t, 10, c.total)
	require.NotNil(t, c.paid)
	assert.Equal(t, 3, *c.paid)
	assert.Equal(t, 3, tx.reads[0].args[2], "n")
	assert.Equal(t, 1, tx.reads[0].args[3], "soldMul")

	_, err = moveHeldCounters(tctx, (&fakeTx{}).rowErr(errDB), "e-1", nil, map[string]int{"c-1": 1}, true)
	assert.ErrorIs(t, err, errDB)
}

func TestCountByCategoryAndArrays(t *testing.T) {
	byCat := countByCategory([]outbound.HoldLine{{CategoryID: "a"}, {CategoryID: "b"}, {CategoryID: "a"}})
	assert.Equal(t, map[string]int{"a": 2, "b": 1}, byCat)
	assert.Equal(t, 3, sum(byCat))
	assert.Equal(t, 0, sum(nil))

	ids, counts := catArrays(byCat)
	require.Len(t, ids, 2)
	got := map[string]int{}
	for i, id := range ids {
		got[id] = counts[i]
	}
	assert.Equal(t, byCat, got, "array paralel: id ke-i berpasangan dengan count ke-i")
}
