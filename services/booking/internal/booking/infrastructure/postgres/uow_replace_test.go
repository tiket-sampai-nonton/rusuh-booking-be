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

var editNow = time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)

// replaceSpec replaces the booking's items with one c-1 line and two c-2 lines.
func replaceSpec() outbound.ReplaceSpec {
	return outbound.ReplaceSpec{
		BookingID: "b-1", AdmissionID: "a-1", EventID: "e-1", BuyerNIKHash: []byte("h"), ExpectedVersion: 2,
		NewAmountMinor: 300, Now: editNow,
		Lines: []outbound.HoldLine{{CategoryID: "c-1"}, {CategoryID: "c-2"}, {CategoryID: "c-2"}},
	}
}

// Edit hanya untuk admission EDITABLE, di dalam TTL + grace, dan di bawah batas edit.
func TestLockEditableAdmission(t *testing.T) {
	live := editNow.Add(time.Minute)
	tests := []struct {
		name    string
		row     []any // status, expires_at, edit_count, cfg_max_edits, cfg_submit_grace_seconds
		rowErr  error
		wantErr error
	}{
		{"boleh edit", []any{"EDITABLE", live, 0, 3, 30}, nil, nil},
		{"lewat TTL tapi dalam grace", []any{"EDITABLE", editNow.Add(-20 * time.Second), 0, 3, 30}, nil, nil},
		{"admission tidak ada", nil, pgx.ErrNoRows, inbound.ErrNotFound},
		{"error database", nil, errDB, errDB},
		{"sudah CONVERTED → terkunci", []any{"CONVERTED", live, 0, 3, 30}, nil, inbound.ErrBookingLocked},
		{"status lain → sesi berakhir", []any{"ACTIVE", live, 0, 3, 30}, nil, inbound.ErrSessionExpired},
		{"lewat TTL + grace", []any{"EDITABLE", editNow.Add(-31 * time.Second), 0, 3, 30}, nil, inbound.ErrSessionExpired},
		{"batas edit tercapai", []any{"EDITABLE", live, 3, 3, 30}, nil, inbound.ErrEditLimit},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx := &fakeTx{}
			if tt.rowErr != nil {
				tx.rowErr(tt.rowErr)
			} else {
				tx.row(tt.row...)
			}
			err := lockEditableAdmission(tctx, tx, "a-1", editNow)
			if tt.wantErr == nil {
				assert.NoError(t, err)
			} else {
				assert.ErrorIs(t, err, tt.wantErr)
			}
		})
	}
}

// Booking yang pembayarannya sudah dimulai terkunci; versi berbeda berarti edit bersamaan.
func TestLockEditableBooking(t *testing.T) {
	v, err := lockEditableBooking(tctx, (&fakeTx{}).row(2, nil), "b-1", 2)
	require.NoError(t, err)
	assert.Equal(t, 2, v)

	_, err = lockEditableBooking(tctx, (&fakeTx{}).rowErr(pgx.ErrNoRows), "b-1", 2)
	assert.ErrorIs(t, err, inbound.ErrNotFound)
	_, err = lockEditableBooking(tctx, (&fakeTx{}).rowErr(errDB), "b-1", 2)
	assert.ErrorIs(t, err, errDB)
	_, err = lockEditableBooking(tctx, (&fakeTx{}).row(2, editNow), "b-1", 2)
	assert.ErrorIs(t, err, inbound.ErrBookingLocked)
	_, err = lockEditableBooking(tctx, (&fakeTx{}).row(3, nil), "b-1", 2)
	assert.ErrorIs(t, err, inbound.ErrVersionConflict)
}

// replaceTx answers an editable admission, a booking at version 2 and two HELD c-1 items.
func replaceTx() *fakeTx {
	return (&fakeTx{}).
		row("EDITABLE", editNow.Add(time.Minute), 0, 3, 30).
		row(2, nil).
		query([]any{"c-1", 2})
}

// Edit berhasil: kategori lama dan baru dikunci, event BookingItemsReplaced, item lama REPLACED, counter per kategori
// bergeser (c-1 −1, c-2 +2), supply dan kuota +1, versi naik, edit_count naik.
func TestReplace_Success(t *testing.T) {
	u, err := NewUnitOfWork(nil)
	require.NoError(t, err)
	tx := replaceTx()

	v, err := u.replace(tctx, tx, replaceSpec())
	require.NoError(t, err)
	assert.Equal(t, 3, v)
	assert.Equal(t, []string{"BookingItemsReplaced@3"}, tx.events())

	// Every old and new category is locked (a new category is currently locked once per line, which is harmless).
	locked := map[any]bool{}
	for _, e := range tx.execs {
		if e.sql == `SELECT 1 FROM booking.category_inventories WHERE category_id=$1::uuid FOR UPDATE` {
			locked[e.args[0]] = true
		}
	}
	assert.Equal(t, map[any]bool{"c-1": true, "c-2": true}, locked)

	deltas := map[any]any{}
	for _, e := range tx.execs {
		if e.sql == `UPDATE booking.category_inventories SET held_quantity=held_quantity+$2, updated_at=now() WHERE category_id=$1::uuid` {
			deltas[e.args[0]] = e.args[1]
		}
	}
	assert.Equal(t, map[any]any{"c-1": -1, "c-2": 2}, deltas)
	assert.Equal(t, []any{"e-1", 1}, tx.exec("UPDATE booking.event_supply").args)
	assert.Equal(t, []any{"e-1", []byte("h"), 1}, tx.exec("UPDATE booking.buyer_quotas").args)
	require.NotNil(t, tx.exec("item_status='REPLACED'"))
	assert.Equal(t, []any{"b-1", 3, 3, int64(300)}, tx.exec("UPDATE booking.bookings").args)
	require.NotNil(t, tx.exec("edit_count=edit_count+1"))
}

// Jumlah tiket sama → supply dan kuota tidak disentuh.
func TestReplace_SameQuantitySkipsSupply(t *testing.T) {
	u, err := NewUnitOfWork(nil)
	require.NoError(t, err)
	tx := replaceTx()
	spec := replaceSpec()
	spec.Lines = spec.Lines[:2] // c-1 + c-2: still 2 units

	_, err = u.replace(tctx, tx, spec)
	require.NoError(t, err)
	assert.Nil(t, tx.exec("UPDATE booking.event_supply"))
	assert.Nil(t, tx.exec("UPDATE booking.buyer_quotas"))
}

// Penolakan admission atau booking terjadi sebelum lock stok.
func TestReplace_RefusedBeforeStockLocks(t *testing.T) {
	u, err := NewUnitOfWork(nil)
	require.NoError(t, err)

	tx := (&fakeTx{}).row("CONVERTED", editNow, 0, 3, 30)
	_, err = u.replace(tctx, tx, replaceSpec())
	assert.ErrorIs(t, err, inbound.ErrBookingLocked)
	assert.Empty(t, tx.execs)

	tx = (&fakeTx{}).row("EDITABLE", editNow.Add(time.Minute), 0, 3, 30).row(5, nil)
	_, err = u.replace(tctx, tx, replaceSpec())
	assert.ErrorIs(t, err, inbound.ErrVersionConflict)
	assert.Empty(t, tx.execs)
}

// Pelanggaran constraint dipetakan: stok kategori → CATEGORY_UNAVAILABLE, batas edit → EDIT_LIMIT, NIK ganda.
func TestReplace_ConstraintErrors(t *testing.T) {
	u, err := NewUnitOfWork(nil)
	require.NoError(t, err)
	tests := []struct {
		name    string
		on      string
		err     error
		wantErr error
	}{
		{"stok kategori habis", "UPDATE booking.category_inventories", pgErr("23514", "category_inventories_check"), inbound.ErrCategoryUnavailable},
		{"supply habis", "UPDATE booking.event_supply", pgErr("23514", "event_supply_check"), inbound.ErrCategoryUnavailable},
		{"kuota NIK", "UPDATE booking.buyer_quotas", pgErr("23514", "buyer_quotas_check"), inbound.ErrBuyerNikLimitReached},
		{"NIK pemegang ganda", "INSERT INTO booking.booking_items", pgErr("23505", "uq_booking_item_nik_active"), inbound.ErrNikDuplicate},
		{"batas edit (race)", "edit_count=edit_count+1", pgErr("23514", "admissions_edit_count"), inbound.ErrEditLimit},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx := replaceTx()
			tx.execErr, tx.execErrOn = tt.err, tt.on
			_, err := u.replace(tctx, tx, replaceSpec())
			assert.ErrorIs(t, err, tt.wantErr)
		})
	}
}
