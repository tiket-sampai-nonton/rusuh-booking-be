package postgres

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/apperror"
	inbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

// Throttle NIK pembeli: backoff eksponensial sejak penolakan terakhir, dibatasi ThrottleMax, dibulatkan ke atas ke detik.
func TestThrottleSeconds(t *testing.T) {
	now := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	spec := outbound.HoldSpec{Now: now, RefusalBudget: 3, ThrottleBase: 2 * time.Second, ThrottleMax: 5 * time.Second}
	ago := func(d time.Duration) *time.Time { t := now.Add(-d); return &t }

	tests := []struct {
		name        string
		budget      int
		refusals    int
		lastRefusal *time.Time
		want        int
	}{
		{"tanpa budget tidak pernah throttle", 0, 10, ago(0), 0},
		{"belum melewati budget", 3, 2, ago(0), 0},
		{"belum pernah ditolak", 3, 3, nil, 0},
		{"langkah pertama: sisa 1,5 detik dibulatkan ke 2", 3, 3, ago(500 * time.Millisecond), 2},
		{"langkah kedua: 4 detik", 3, 4, ago(0), 4},
		{"dibatasi ThrottleMax", 3, 10, ago(0), 5},
		{"sisa di bawah satu detik tetap 1", 3, 3, ago(1900 * time.Millisecond), 1},
		{"jendela sudah lewat", 3, 3, ago(2 * time.Second), 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := spec
			s.RefusalBudget = tt.budget
			assert.Equal(t, tt.want, throttleSeconds(s, tt.refusals, tt.lastRefusal))
		})
	}
}

var holdNow = time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)

// holdSpec is a hold of n lines in category c-1, edit enabled, refusal budget 3.
func holdSpec(n int) outbound.HoldSpec {
	lines := make([]outbound.HoldLine, n)
	for i := range lines {
		lines[i] = outbound.HoldLine{ItemID: fmt.Sprintf("i-%d", i), CategoryID: "c-1", UnitPriceMinor: 100, AttendeeNIKHash: []byte("nh")}
	}
	return outbound.HoldSpec{
		BookingID: "b-1", PublicRef: "R-1", EventID: "e-1", AdmissionID: "a-1", VisitorID: "v-1", BuyerNIKHash: []byte("h"),
		AmountMinor: int64(100 * n), Lines: lines, CfgEditEnabled: true, Now: holdNow,
		RefusalBudget: 3, ThrottleBase: 2 * time.Second, ThrottleMax: time.Minute,
	}
}

// admissionRow is the lockHoldAdmission row: status, expires_at, grace 60 s, outstanding units, refusals,
// last refusal, event status.
func admissionRow(status string, expiresAt time.Time, outstanding, refusals int, lastRefusal any, evStatus string) []any {
	return []any{status, expiresAt, 60, outstanding, refusals, lastRefusal, evStatus}
}

// Guard hold yang menolak sebelum stok ditulis: sesi, jumlah tiket, dan event batal.
func TestHold_Refusals(t *testing.T) {
	u, err := NewUnitOfWork(nil)
	require.NoError(t, err)
	live := holdNow.Add(time.Minute)

	tests := []struct {
		name    string
		n       int
		adm     []any
		admErr  error
		quota   []any // nil: the hold must stop before the quota row
		wantErr error
	}{
		{"admission tidak ada atau milik visitor lain", 1, nil, pgx.ErrNoRows, nil, inbound.ErrSessionExpired},
		{"admission tidak aktif", 1, admissionRow("RESERVED", live, 2, 0, nil, "ON_SALE"), nil, nil, inbound.ErrSessionExpired},
		{"lewat TTL + grace", 1, admissionRow("ACTIVE", holdNow.Add(-61*time.Second), 2, 0, nil, "ON_SALE"), nil, nil, inbound.ErrSessionExpired},
		{"tanpa baris tiket", 0, admissionRow("ACTIVE", live, 2, 0, nil, "ON_SALE"), nil, nil, inbound.ErrQuantityInvalid},
		{"melebihi unit milik admission (F1)", 3, admissionRow("ACTIVE", live, 2, 0, nil, "ON_SALE"), nil, nil, inbound.ErrQuantityInvalid},
		{"event batal sebelum lock admission", 1, admissionRow("ACTIVE", live, 2, 0, nil, "CANCELLED"), nil, nil, inbound.ErrEventCancelled},
		{"event batal setelah lock supply (P2-9)", 1, admissionRow("EDITABLE", live, 2, 0, nil, "ON_SALE"), nil, []any{0, 0, 3, "CANCELLED"}, inbound.ErrEventCancelled},
		{"error database diteruskan", 1, nil, errDB, nil, errDB},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx := &fakeTx{}
			if tt.admErr != nil {
				tx.rowErr(tt.admErr)
			} else {
				tx.row(tt.adm...)
			}
			if tt.quota != nil {
				tx.row(tt.quota...)
			}
			_, err := u.hold(tctx, tx, holdSpec(tt.n))
			assert.ErrorIs(t, err, tt.wantErr)
			if tt.quota == nil {
				assert.Empty(t, tx.execs, "no lock or write after an early refusal")
			} else {
				assert.Len(t, tx.execs, 1, "only the supply lock")
			}
		})
	}
}

// holdUpToCap runs hold through the admission, supply, quota and category steps with the given state.
func holdUpToCap(t *testing.T, refusals int, lastRefusal any, quota []any, avail int) (*fakeTx, outbound.HoldResult, error) {
	t.Helper()
	u, err := NewUnitOfWork(nil)
	require.NoError(t, err)
	tx := (&fakeTx{queries: []*txRows{{data: [][]any{{"c-1", avail}}}}}).
		row(admissionRow("ACTIVE", holdNow.Add(time.Minute), 2, refusals, lastRefusal, "ON_SALE")...).
		row(quota...)
	res, err := u.hold(tctx, tx, holdSpec(2))
	return tx, res, err
}

// Stok kategori kurang → CATEGORY_UNAVAILABLE dengan id kategori; transaksi di-rollback (error bertipe).
func TestHold_CategoryUnavailable(t *testing.T) {
	_, _, err := holdUpToCap(t, 0, nil, []any{0, 0, 3, "ON_SALE"}, 1)
	var ae *apperror.Error
	require.True(t, errors.As(err, &ae))
	assert.Equal(t, inbound.CodeCategoryUnavailable, ae.Code)
	assert.Equal(t, "c-1", ae.Message)
}

// Budget penolakan habis dan masih di jendela backoff → BUYER_NIK_THROTTLED dengan retryAfter.
func TestHold_Throttled(t *testing.T) {
	_, _, err := holdUpToCap(t, 3, holdNow.Add(-time.Second), []any{0, 0, 3, "ON_SALE"}, 5)
	var ae *apperror.Error
	require.True(t, errors.As(err, &ae))
	assert.Equal(t, inbound.CodeBuyerNikThrottled, ae.Code)
	assert.Equal(t, "1", ae.Details["retryAfter"])
}

// Kuota NIK pembeli penuh → hasil Refused (commit) dan counter penolakan naik dengan jam aplikasi.
func TestHold_BuyerNikCapRefused(t *testing.T) {
	tx, res, err := holdUpToCap(t, 0, nil, []any{1, 1, 3, "ON_SALE"}, 5)
	require.NoError(t, err)
	assert.Equal(t, outbound.RefusedBuyerNikLimit, res.Refused)
	require.Len(t, tx.execs, 2, "supply lock + refusal counter")
	assert.Contains(t, tx.execs[1].sql, "buyer_nik_refusals = buyer_nik_refusals + 1")
	assert.Equal(t, []any{"a-1", holdNow}, tx.execs[1].args)
}

func TestLockHoldAdmission(t *testing.T) {
	last := holdNow.Add(-time.Minute)
	a, err := lockHoldAdmission(tctx, (&fakeTx{}).row(admissionRow("ACTIVE", holdNow, 2, 4, last, "ON_SALE")...), holdSpec(1))
	require.NoError(t, err)
	assert.Equal(t, holdAdmission{status: "ACTIVE", eventStatus: "ON_SALE", expiresAt: holdNow, grace: time.Minute,
		outstanding: 2, refusals: 4, lastRefusal: &last}, a)
}

func TestLockBuyerQuota(t *testing.T) {
	q, err := lockBuyerQuota(tctx, (&fakeTx{}).row(1, 2, 3, "ON_SALE"), "e-1", []byte("h"))
	require.NoError(t, err)
	assert.Equal(t, buyerQuota{held: 1, paid: 2, max: 3, eventStatus: "ON_SALE"}, q)
}

// Kategori yang tidak ditemukan dihitung tersedia 0; kategori yang cukup tidak ikut ditolak.
func TestCheckCategoryStock(t *testing.T) {
	tx := &fakeTx{queries: []*txRows{{data: [][]any{{"c-1", 5}}}}}
	assert.NoError(t, checkCategoryStock(tctx, tx, map[string]int{"c-1": 5}))

	tx = &fakeTx{queries: []*txRows{{data: [][]any{{"c-1", 5}}}}}
	err := checkCategoryStock(tctx, tx, map[string]int{"c-1": 1, "c-2": 1})
	var ae *apperror.Error
	require.True(t, errors.As(err, &ae))
	assert.Equal(t, "c-2", ae.Message)

	tx = &fakeTx{queries: []*txRows{{err: errDB}}}
	assert.ErrorIs(t, checkCategoryStock(tctx, tx, map[string]int{"c-1": 1}), errDB)
	assert.Error(t, checkCategoryStock(tctx, &fakeTx{}, map[string]int{"c-1": 1}))
}

// Booking dan item ditulis dalam satu statement; id item kembali sesuai urutan baris; pelanggaran unik dipetakan.
func TestInsertHeldBooking(t *testing.T) {
	tx := &fakeTx{}
	ids, err := insertHeldBooking(tctx, tx, holdSpec(2), 2)
	require.NoError(t, err)
	assert.Equal(t, []string{"i-0", "i-1"}, ids)
	require.Len(t, tx.execs, 1)
	args := tx.execs[0].args
	assert.Equal(t, 2, args[8], "item_quantity")
	assert.Equal(t, 2, args[10], "version")
	assert.Equal(t, []string{"i-0", "i-1"}, args[12])
	assert.Equal(t, []string{"c-1", "c-1"}, args[13])

	_, err = insertHeldBooking(tctx, &fakeTx{execErr: pgErr("23505", "uq_attendee_nik_per_event")}, holdSpec(1), 1)
	assert.ErrorIs(t, err, inbound.ErrNikRegistered)
}

// Counter: kategori +held, kuota +n, supply allocated −outstanding milik admission, held +n.
func TestAddHeldCounters(t *testing.T) {
	tx := &fakeTx{}
	require.NoError(t, addHeldCounters(tctx, tx, holdSpec(2), map[string]int{"c-1": 2}, 3))
	args := tx.execs[0].args
	assert.Equal(t, []string{"c-1"}, args[0])
	assert.Equal(t, []int{2}, args[1])
	assert.Equal(t, 2, args[2], "n")
	assert.Equal(t, 3, args[5], "outstanding")

	err := addHeldCounters(tctx, &fakeTx{execErr: pgErr("23514", "category_inventories_check")}, holdSpec(1), map[string]int{"c-1": 1}, 1)
	assert.ErrorIs(t, err, inbound.ErrCategoryUnavailable)
}

// Admission dikunci langsung (CONVERTED + alasan) atau tetap EDITABLE; keduanya mengikat pembeli dan menolkan unit.
func TestBindHoldAdmission(t *testing.T) {
	tx := &fakeTx{}
	require.NoError(t, bindHoldAdmission(tctx, tx, holdSpec(1), true, "SELECTION_EXPIRED"))
	assert.Contains(t, tx.execs[0].sql, "status='CONVERTED'")
	assert.Equal(t, "SELECTION_EXPIRED", tx.execs[0].args[1])

	tx = &fakeTx{}
	require.NoError(t, bindHoldAdmission(tctx, tx, holdSpec(1), false, ""))
	assert.Contains(t, tx.execs[0].sql, "status='EDITABLE'")
	assert.Contains(t, tx.execs[0].sql, "outstanding_units=0")
}

// Hold berhasil: event lebih dulu (versi akhir langsung di baris booking), lalu booking+item, counter, dan admission.
// Edit nonaktif atau hold di masa grace mengunci admission seketika (BookingLocked versi 2).
func TestHold_Success(t *testing.T) {
	tests := []struct {
		name        string
		editEnabled bool
		expiresAt   time.Time
		wantEvents  []string
		wantVersion int
		wantAdm     string
		wantReason  any
	}{
		{"edit aktif → EDITABLE", true, holdNow.Add(time.Minute), []string{"BookingHeld@1"}, 1, "status='EDITABLE'", nil},
		{"edit nonaktif → CONVERTED", false, holdNow.Add(time.Minute), []string{"BookingHeld@1", "BookingLocked@2"}, 2, "status='CONVERTED'", "EDIT_DISABLED"},
		{"masa grace → CONVERTED", true, holdNow.Add(-10 * time.Second), []string{"BookingHeld@1", "BookingLocked@2"}, 2, "status='CONVERTED'", "SELECTION_EXPIRED"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, err := NewUnitOfWork(nil)
			require.NoError(t, err)
			tx := (&fakeTx{}).
				row(admissionRow("ACTIVE", tt.expiresAt, 2, 0, nil, "ON_SALE")...).
				row(0, 0, 3, "ON_SALE").
				query([]any{"c-1", 5})
			spec := holdSpec(2)
			spec.CfgEditEnabled = tt.editEnabled

			res, err := u.hold(tctx, tx, spec)
			require.NoError(t, err)
			assert.Equal(t, tt.wantEvents, tx.events())
			assert.Equal(t, outbound.HoldResult{BookingID: "b-1", PublicRef: "R-1", Version: tt.wantVersion, Status: "PENDING_PAYMENT",
				AmountMinor: 200, ItemIDs: []string{"i-0", "i-1"}}, res)
			insert := tx.exec("INSERT INTO booking.bookings")
			require.NotNil(t, insert)
			assert.Equal(t, tt.wantVersion, insert.args[10], "booking row written at the final version")
			require.NotNil(t, tx.exec("UPDATE booking.event_supply"))
			adm := tx.exec(tt.wantAdm)
			require.NotNil(t, adm)
			if tt.wantReason != nil {
				assert.Equal(t, tt.wantReason, adm.args[1])
			}
		})
	}
}

// Konflik versi event store (stream sudah ada) menjadi ErrConcurrency agar transaksi diulang.
func TestHold_EventConflictIsRetryable(t *testing.T) {
	u, err := NewUnitOfWork(nil)
	require.NoError(t, err)
	tx := (&fakeTx{execErr: pgErr("23505", "booking_events_pkey"), execErrOn: "INSERT INTO booking_events"}).
		row(admissionRow("ACTIVE", holdNow.Add(time.Minute), 2, 0, nil, "ON_SALE")...).
		row(0, 0, 3, "ON_SALE").
		query([]any{"c-1", 5})
	_, err = u.hold(tctx, tx, holdSpec(1))
	assert.True(t, retryable(err))
	assert.Nil(t, tx.exec("INSERT INTO booking.bookings"), "nothing written after the failed append")
}
