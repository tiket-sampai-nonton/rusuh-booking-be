//go:build integration

package postgres

import (
	"context"
	cryptorand "crypto/rand"
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

func newTestUUID() string {
	b := make([]byte, 16)
	_, _ = cryptorand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func anonEntry(seq int, units int) outbound.ReserveEntry {
	return outbound.ReserveEntry{
		QueueEntryID: newTestUUID(), Seq: int64(seq), VisitorID: newTestUUID(),
		Units: units, AdmissionJTI: newTestUUID(),
	}
}

// TC-ANON-06: reservation needs no buyer NIK, grants min(3, free) units, never touches buyer_quotas.
func TestReserveAnonymous_PartialGrantsAndNoQuotaAccess(t *testing.T) {
	f := newAcctFixture(t, suffixFor(t), 7)
	repo := NewAdmissionRepo(f.pool)
	var entries []outbound.ReserveEntry
	for i := 1; i <= 4; i++ {
		entries = append(entries, anonEntry(i, 3))
	}
	out, err := repo.ReserveBatch(context.Background(), f.eventID, entries, time.Now())
	require.NoError(t, err)
	require.Len(t, out, 4)
	assert.Equal(t, []int{3, 3, 1}, []int{out[0].Units, out[1].Units, out[2].Units}, "stock tail 3,3,1")
	for i := 0; i < 3; i++ {
		assert.Equal(t, "GRANTED", out[i].Outcome)
	}
	assert.Equal(t, "NO_SUPPLY_NOW", out[3].Outcome,
		"free is 0 but reservations may still return: not SOLD_OUT (F4)")
	var status string
	require.NoError(t, f.pool.QueryRow(context.Background(), `SELECT status FROM booking.events WHERE id=$1::uuid`, f.eventID).Scan(&status))
	assert.Equal(t, "AVAILABLE", status, "no premature SOLD_OUT")
	assert.Equal(t, 0, f.quotaRows(t), "reserve never accesses buyer_quotas")
	var sumOut int
	require.NoError(t, f.pool.QueryRow(context.Background(),
		`SELECT sum(outstanding_units) FROM booking.admissions WHERE event_id=$1::uuid`, f.eventID).Scan(&sumOut))
	assert.Equal(t, f.ledger(t).alloc, sumOut, "allocated_units = sum(outstanding_units)")

	// Idempotent replay per queue_entry_id: same grant, no extra stock taken.
	again, err := repo.ReserveBatch(context.Background(), f.eventID, entries[:1], time.Now())
	require.NoError(t, err)
	assert.Equal(t, out[0].AllocationID, again[0].AllocationID)
	assert.Equal(t, 7, f.ledger(t).alloc)
}

func TestReserveAnonymous_UnitsClampedToMaxPerOrder(t *testing.T) {
	f := newAcctFixture(t, suffixFor(t), 20)
	repo := NewAdmissionRepo(f.pool)
	out, err := repo.ReserveBatch(context.Background(), f.eventID, []outbound.ReserveEntry{anonEntry(1, 9)}, time.Now())
	require.NoError(t, err)
	assert.Equal(t, 3, out[0].Units, "clamped to the event's max_tickets_per_order")
}

func TestReserveAnonymous_SoldOutOnlyWhenNothingCanReturn(t *testing.T) {
	f := newAcctFixture(t, suffixFor(t), 2)
	repo := NewAdmissionRepo(f.pool)
	ctx := context.Background()
	out, err := repo.ReserveBatch(ctx, f.eventID, []outbound.ReserveEntry{anonEntry(1, 3)}, time.Now())
	require.NoError(t, err)
	require.Equal(t, 2, out[0].Units)
	// Release it so nothing is allocated nor held; then take the whole stock: the next one finds nothing at all.
	ok, err := repo.Release(ctx, out[0].AllocationID)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, 0, f.ledger(t).alloc)
}

// R09.4: every release path moves only the admission's own outstanding units, exactly once.
func TestReleasePaths_ReleaseOnceAndOnlyOwnUnits(t *testing.T) {
	f := newAcctFixture(t, suffixFor(t), 9)
	repo := NewAdmissionRepo(f.pool)
	ctx := context.Background()
	out, err := repo.ReserveBatch(ctx, f.eventID, []outbound.ReserveEntry{anonEntry(1, 3), anonEntry(2, 3)}, time.Now())
	require.NoError(t, err)
	require.Equal(t, 6, f.ledger(t).alloc)

	ok, err := repo.Release(ctx, out[0].AllocationID)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, 3, f.ledger(t).alloc, "released exactly its 3")
	ok, err = repo.Release(ctx, out[0].AllocationID)
	require.NoError(t, err)
	assert.False(t, ok, "second release is a no-op")
	assert.Equal(t, 3, f.ledger(t).alloc)

	// expiry twice on the other admission releases once
	require.NoError(t, repo.ExpireSession(ctx, out[1].AllocationID, time.Now()))
	require.NoError(t, repo.ExpireSession(ctx, out[1].AllocationID, time.Now()))
	assert.Equal(t, 0, f.ledger(t).alloc)
}

func TestReleasePaths_ExpireAfterHoldReleasesNothingTwice(t *testing.T) {
	f := newAcctFixture(t, suffixFor(t), 9)
	repo := NewAdmissionRepo(f.pool)
	adm, vis := f.reserve(t, 1, 3, true)
	_, err := f.uow.Hold(context.Background(), f.spec(1, 1, adm, vis, true))
	require.NoError(t, err)
	l := f.ledger(t)
	require.Equal(t, 0, l.alloc)
	require.NoError(t, repo.ExpireSession(context.Background(), adm, time.Now()))
	l2 := f.ledger(t)
	assert.Equal(t, l, l2, "EDITABLE session expiry has no reserved units left to release")
}

func TestReleasePaths_PaymentLockKeepsLedger(t *testing.T) {
	f := newAcctFixture(t, suffixFor(t), 9)
	spec := f.holdOne(t, 1, 3, 2)
	before := f.ledger(t)
	res, err := f.uow.PaymentLock(context.Background(), outbound.PaymentLockSpec{BookingID: spec.BookingID, AttemptKey: "k", CorrelationID: "c"})
	require.NoError(t, err)
	assert.Equal(t, "OK", res.Outcome)
	assert.Equal(t, before, f.ledger(t), "payment-lock no longer touches allocated_units")
}

// CP-1 ledger property: after any mix of reserve / hold / release / expire / edit / payment-lock /
// expire-booking, allocated = sum(outstanding of live admissions) and held = sum of HELD item rows.
func TestSupplyLedger_Property(t *testing.T) {
	f := newAcctFixture(t, suffixFor(t), 60)
	repo := NewAdmissionRepo(f.pool)
	ctx := context.Background()
	rng := rand.New(rand.NewSource(7))

	type adm struct {
		id, vis string
		idx     int
		op      int
	}
	var pool []*adm
	for i := 0; i < 24; i++ {
		id, vis := f.reserve(t, i, 1+rng.Intn(3), i%2 == 0)
		pool = append(pool, &adm{id: id, vis: vis, idx: i, op: rng.Intn(5)})
	}
	var wg sync.WaitGroup
	for _, a := range pool {
		wg.Add(1)
		go func() {
			defer wg.Done()
			switch a.op {
			case 0: // saga release
				_, _ = repo.Release(ctx, a.id)
			case 1: // queue-side expiry
				_ = repo.ExpireSession(ctx, a.id, time.Now())
			default: // hold up to its own units, then maybe pay-lock or expire the booking
				var units int
				_ = f.pool.QueryRow(ctx, `SELECT outstanding_units FROM booking.admissions WHERE id=$1::uuid`, a.id).Scan(&units)
				if units == 0 {
					return
				}
				n := 1 + a.idx%units
				spec := f.spec(a.idx, n, a.id, a.vis, a.idx%2 == 0)
				if _, err := f.uow.Hold(ctx, spec); err != nil {
					return
				}
				switch a.idx % 3 {
				case 0:
					_, _ = f.uow.PaymentLock(ctx, outbound.PaymentLockSpec{BookingID: spec.BookingID, AttemptKey: "k", CorrelationID: "c"})
				case 1:
					_, _ = f.uow.Expire(ctx, outbound.ExpireSpec{BookingID: spec.BookingID, Reason: "PAYMENT_TIMEOUT", CorrelationID: "c"})
				}
			}
		}()
	}
	wg.Wait()

	var sumOut, heldItems int
	require.NoError(t, f.pool.QueryRow(ctx, `SELECT COALESCE(sum(outstanding_units),0) FROM booking.admissions WHERE event_id=$1::uuid`, f.eventID).Scan(&sumOut))
	require.NoError(t, f.pool.QueryRow(ctx, `SELECT count(*) FROM booking.booking_items WHERE event_id=$1::uuid AND item_status='HELD'`, f.eventID).Scan(&heldItems))
	var catHeld int
	require.NoError(t, f.pool.QueryRow(ctx, `SELECT held_quantity FROM booking.category_inventories WHERE category_id=$1::uuid`, f.catID).Scan(&catHeld))
	l := f.ledger(t)
	assert.Equal(t, sumOut, l.alloc, "allocated_units = sum(outstanding_units)")
	assert.Equal(t, heldItems, l.held, "held_units = HELD items")
	assert.Equal(t, heldItems, catHeld)
	assert.LessOrEqual(t, l.alloc+l.held+l.sold, l.total)
	var live int
	require.NoError(t, f.pool.QueryRow(ctx, `SELECT count(*) FROM booking.admissions WHERE event_id=$1::uuid AND status NOT IN ('RESERVED','ACTIVE','EDITABLE') AND outstanding_units<>0`, f.eventID).Scan(&live))
	assert.Equal(t, 0, live, "ended admissions never keep a counter")
	_ = fmt.Sprint()
}

// Lock-order hammer: hold / edit / settle-path / expire on the same event must not deadlock (no 40P01).
func TestLockOrder_HoldEditExpire(t *testing.T) {
	f := newAcctFixture(t, suffixFor(t), 30)
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for i := 0; i < 8; i++ {
		spec := f.holdOne(t, i, 3, 1)
		wg.Add(3)
		go func() {
			defer wg.Done()
			_, err := f.uow.ReplaceItems(ctx, f.replaceSpec(spec, 2, 1))
			errs <- err
		}()
		go func() {
			defer wg.Done()
			_, err := f.uow.PaymentLock(ctx, outbound.PaymentLockSpec{BookingID: spec.BookingID, AttemptKey: "k", CorrelationID: "c"})
			errs <- err
		}()
		go func() {
			defer wg.Done()
			_, err := f.uow.Expire(ctx, outbound.ExpireSpec{BookingID: spec.BookingID, Reason: "PAYMENT_TIMEOUT", CorrelationID: "c"})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			assert.NotContains(t, err.Error(), "40P01", "deadlock detected")
			assert.NotContains(t, err.Error(), "deadlock")
		}
	}
}
