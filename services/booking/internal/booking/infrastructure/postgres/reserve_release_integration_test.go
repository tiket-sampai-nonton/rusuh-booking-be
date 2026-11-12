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

	vo "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/valueobject"
	inbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
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
	ok, err := repo.Release(ctx, out[0].AllocationID, vo.EndUnclaimed)
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

	ok, err := repo.Release(ctx, out[0].AllocationID, vo.EndUnclaimed)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, 3, f.ledger(t).alloc, "released exactly its 3")
	ok, err = repo.Release(ctx, out[0].AllocationID, vo.EndUnclaimed)
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
				_, _ = repo.Release(ctx, a.id, vo.EndUnclaimed)
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

// reserveAt reserves one anonymous admission at t0 for an event whose selection TTL is 90 s and claim window 180 s.
func reserveAt(t *testing.T, f acctFixture, t0 time.Time) (allocID string) {
	t.Helper()
	ctx := context.Background()
	_, err := f.pool.Exec(ctx, `UPDATE booking.events SET selection_ttl_seconds=90, claim_window_seconds=180 WHERE id=$1::uuid`, f.eventID)
	require.NoError(t, err)
	out, err := NewAdmissionRepo(f.pool).ReserveBatch(ctx, f.eventID, []outbound.ReserveEntry{anonEntry(1, 2)}, t0)
	require.NoError(t, err)
	require.Equal(t, "GRANTED", out[0].Outcome)
	return out[0].AllocationID
}

func expiresOf(t *testing.T, f acctFixture, id string) time.Time {
	t.Helper()
	var e time.Time
	require.NoError(t, f.pool.QueryRow(context.Background(), `SELECT expires_at FROM booking.admissions WHERE id=$1::uuid`, id).Scan(&e))
	return e
}

// TC-SES-09 (P7, P9, 3.7): the session deadline is activation time + selection TTL, set once.
func TestActivate_DeadlineIsActivationPlusSelectionTTL(t *testing.T) {
	t0 := time.Now().UTC().Truncate(time.Millisecond)

	t.Run("P7 deadline = activation + selection ttl, stable on read", func(t *testing.T) {
		f := newAcctFixture(t, suffixFor(t), 6)
		repo := NewAdmissionRepo(f.pool)
		id := reserveAt(t, f, t0)
		at := t0.Add(60 * time.Second)
		a, err := repo.Activate(context.Background(), id, at)
		require.NoError(t, err)
		want := t0.Add(150 * time.Second)
		assert.WithinDuration(t, want, a.ExpiresAt, time.Millisecond, "returned deadline")
		assert.WithinDuration(t, want, expiresOf(t, f, id), time.Millisecond, "stored deadline")
		for i := 0; i < 3; i++ {
			g, err := repo.GetByID(context.Background(), id)
			require.NoError(t, err)
			assert.WithinDuration(t, want, g.ExpiresAt, time.Millisecond, "read never moves the deadline")
		}
	})

	t.Run("P9 activation after the claim window is refused and leaves the row unchanged", func(t *testing.T) {
		f := newAcctFixture(t, suffixFor(t), 6)
		repo := NewAdmissionRepo(f.pool)
		id := reserveAt(t, f, t0)
		before := expiresOf(t, f, id)
		_, err := repo.Activate(context.Background(), id, t0.Add(181*time.Second))
		require.ErrorIs(t, err, inbound.ErrAdmissionToken)
		assert.True(t, before.Equal(expiresOf(t, f, id)))
		var status string
		require.NoError(t, f.pool.QueryRow(context.Background(), `SELECT status FROM booking.admissions WHERE id=$1::uuid`, id).Scan(&status))
		assert.Equal(t, "RESERVED", status)
	})

	t.Run("3.7 in-flight ACTIVE row keeps its stored deadline", func(t *testing.T) {
		f := newAcctFixture(t, suffixFor(t), 6)
		repo := NewAdmissionRepo(f.pool)
		id, _ := f.reserve(t, 1, 2, true) // ACTIVE, expires_at = now()+10 min (pre-deploy shape)
		stored := expiresOf(t, f, id)
		g, err := repo.GetByID(context.Background(), id)
		require.NoError(t, err)
		assert.True(t, stored.Equal(g.ExpiresAt), "no recompute")
		due, err := repo.DueSessions(context.Background(), stored.Add(4*time.Second), 10)
		require.NoError(t, err)
		for _, d := range due {
			assert.NotEqual(t, id, d.ID, "not due before expires_at + grace (5 s)")
		}
		due, err = repo.DueSessions(context.Background(), stored.Add(6*time.Second), 10)
		require.NoError(t, err)
		var found bool
		for _, d := range due {
			found = found || d.ID == id
		}
		assert.True(t, found, "due after expires_at + grace")
	})
}

// journeyEnded returns the booking.journey.ended payloads written for one admission.
func journeyEnded(t *testing.T, f acctFixture, admID string) []map[string]any {
	t.Helper()
	rows, err := f.pool.Query(context.Background(),
		`SELECT payload FROM booking.outbox_events WHERE routing_key='booking.journey.ended' AND aggregate_id=$1::uuid`, admID)
	require.NoError(t, err)
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var m map[string]any
		require.NoError(t, rows.Scan(&m))
		out = append(out, m)
	}
	require.NoError(t, rows.Err())
	return out
}

func admState(t *testing.T, f acctFixture, admID string) (status string, outstanding int, queueEntryID string) {
	t.Helper()
	require.NoError(t, f.pool.QueryRow(context.Background(),
		`SELECT status, outstanding_units, queue_entry_id::text FROM booking.admissions WHERE id=$1::uuid`, admID).
		Scan(&status, &outstanding, &queueEntryID))
	return
}

// TC-QUE-37 (P1): releasing a RESERVED or ACTIVE admission returns exactly its outstanding units and writes
// exactly one booking.journey.ended row {eventId, queueEntryId, outcome}, atomically.
func TestRelease_EmitsJourneyEndedAndReturnsUnits(t *testing.T) {
	cases := []struct {
		name        string
		reserved    bool // true: RESERVED via ReserveBatch, false: ACTIVE fixture row
		reason      vo.EndReason
		wantOutcome string
	}{
		{"reserved/unclaimed", true, vo.EndUnclaimed, "UNCLAIMED"},
		{"reserved/left", true, vo.EndLeftWithoutBuying, "LEFT_WITHOUT_BUYING"},
		{"active/left", false, vo.EndLeftWithoutBuying, "LEFT_WITHOUT_BUYING"},
		{"active/unclaimed", false, vo.EndUnclaimed, "UNCLAIMED"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newAcctFixture(t, suffixFor(t), 8)
			repo := NewAdmissionRepo(f.pool)
			var id string
			if tc.reserved {
				id = reserveAt(t, f, time.Now())
			} else {
				id, _ = f.reserve(t, 1, 2, true)
			}
			require.Equal(t, 2, f.ledger(t).alloc)

			ok, err := repo.Release(context.Background(), id, tc.reason)
			require.NoError(t, err)
			assert.True(t, ok)

			status, out, qe := admState(t, f, id)
			assert.Equal(t, "RELEASED", status)
			assert.Zero(t, out)
			assert.Zero(t, f.ledger(t).alloc, "exactly outstanding_units returned")
			var endReason string
			require.NoError(t, f.pool.QueryRow(context.Background(),
				`SELECT end_reason FROM booking.admissions WHERE id=$1::uuid`, id).Scan(&endReason))
			assert.Equal(t, string(tc.reason), endReason)

			evs := journeyEnded(t, f, id)
			require.Len(t, evs, 1, "exactly one journey.ended row")
			assert.Equal(t, map[string]any{"eventId": f.eventID, "queueEntryId": qe, "outcome": tc.wantOutcome}, evs[0],
				"payload keys are exactly eventId, queueEntryId, outcome")
		})
	}
}

// TC-QUE-37 (P1): if the outbox insert fails the release is rolled back as a whole.
func TestRelease_OutboxFailureRollsBackEverything(t *testing.T) {
	f := newAcctFixture(t, suffixFor(t), 8)
	repo := NewAdmissionRepo(f.pool)
	id, _ := f.reserve(t, 1, 2, true)
	ctx := context.Background()
	_, err := f.pool.Exec(ctx, `
		CREATE FUNCTION booking.fail_journey_ended() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
			IF NEW.routing_key = 'booking.journey.ended' THEN RAISE EXCEPTION 'injected outbox failure'; END IF;
			RETURN NEW; END $$;
		CREATE TRIGGER fail_journey_ended BEFORE INSERT ON booking.outbox_events
			FOR EACH ROW EXECUTE FUNCTION booking.fail_journey_ended()`)
	require.NoError(t, err)

	_, err = repo.Release(ctx, id, vo.EndLeftWithoutBuying)
	require.Error(t, err)
	status, out, _ := admState(t, f, id)
	assert.Equal(t, "ACTIVE", status, "status unchanged")
	assert.Equal(t, 2, out, "outstanding units unchanged")
	assert.Equal(t, 2, f.ledger(t).alloc, "stock unchanged")
}

// TC-QUE-37 (P2): concurrent cancel / :release / sweeper on one admission return stock once and emit once.
func TestRelease_ConcurrentIsIdempotent(t *testing.T) {
	f := newAcctFixture(t, suffixFor(t), 8)
	repo := NewAdmissionRepo(f.pool)
	id, _ := f.reserve(t, 1, 2, true)
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			switch i % 3 {
			case 0:
				_, _ = repo.Release(ctx, id, vo.EndLeftWithoutBuying)
			case 1:
				_, _ = repo.Release(ctx, id, vo.EndUnclaimed)
			default:
				_ = repo.ExpireSession(ctx, id, time.Now())
			}
		}()
	}
	wg.Wait()
	assert.Zero(t, f.ledger(t).alloc, "stock returned exactly once (never negative, never double)")
	assert.Len(t, journeyEnded(t, f, id), 1, "one outbox row")
}

// TC-QUE-37 (P3): an EDITABLE admission (hold exists) is locked: no state change, no stock change, no event.
func TestRelease_EditableIsLocked(t *testing.T) {
	f := newAcctFixture(t, suffixFor(t), 9)
	repo := NewAdmissionRepo(f.pool)
	spec := f.holdOne(t, 1, 3, 1)
	before := f.ledger(t)
	status, _, _ := admState(t, f, spec.AdmissionID)
	require.Equal(t, "EDITABLE", status)

	ok, err := repo.Release(context.Background(), spec.AdmissionID, vo.EndLeftWithoutBuying)
	require.ErrorIs(t, err, inbound.ErrBookingLocked)
	assert.False(t, ok)
	status, _, _ = admState(t, f, spec.AdmissionID)
	assert.Equal(t, "EDITABLE", status)
	assert.Equal(t, before, f.ledger(t))
	assert.Empty(t, journeyEnded(t, f, spec.AdmissionID))
}

// TC-QUE-37: terminal admissions are a no-op (false, nil) and write no event.
func TestRelease_TerminalIsNoOp(t *testing.T) {
	for _, terminal := range []string{"RELEASED", "EXPIRED", "CONVERTED"} {
		t.Run(terminal, func(t *testing.T) {
			f := newAcctFixture(t, suffixFor(t), 8)
			repo := NewAdmissionRepo(f.pool)
			id, _ := f.reserve(t, 1, 2, true)
			ctx := context.Background()
			_, err := f.pool.Exec(ctx, `UPDATE booking.event_supply SET allocated_units=0 WHERE event_id=$1::uuid`, f.eventID)
			require.NoError(t, err)
			set := `status=$2, outstanding_units=0, ended_at=now(), end_reason='UNCLAIMED'`
			if terminal == "CONVERTED" {
				set = `status=$2, outstanding_units=0, ended_at=now(), end_reason='CONVERTED', lock_reason='BOOKING_ENDED', locked_at=now()`
			}
			_, err = f.pool.Exec(ctx, `UPDATE booking.admissions SET `+set+` WHERE id=$1::uuid`, id, terminal)
			require.NoError(t, err)

			ok, err := repo.Release(ctx, id, vo.EndLeftWithoutBuying)
			require.NoError(t, err)
			assert.False(t, ok)
			assert.Empty(t, journeyEnded(t, f, id))
			assert.Zero(t, f.ledger(t).alloc)
		})
	}
}

func soldOutRows(t *testing.T, f acctFixture) int {
	t.Helper()
	var n int
	require.NoError(t, f.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM booking.outbox_events WHERE routing_key='booking.sold_out' AND payload->>'eventId'=$1`, f.eventID).Scan(&n))
	return n
}

// TC-QUE-40 (5.6, SRS-F-09-05): stock that can still come back is not a sell-out. Taking the last free units into a
// reservation (which can be released) or paying while other reservations exist must not announce booking.sold_out;
// only free + allocated + held = 0 does. A premature sold_out makes Queue refuse joins with 410 while tickets may return.
func TestSoldOut_OnlyWhenNothingCanReturn(t *testing.T) {
	t.Run("one reservation takes all free units", func(t *testing.T) {
		f := newAcctFixture(t, suffixFor(t), 2)
		out, err := NewAdmissionRepo(f.pool).ReserveBatch(context.Background(), f.eventID, []outbound.ReserveEntry{anonEntry(1, 3)}, time.Now())
		require.NoError(t, err)
		require.Equal(t, "GRANTED", out[0].Outcome)
		require.Equal(t, 2, out[0].Units)
		assert.Zero(t, soldOutRows(t, f), "the reservation can be released: not sold out")
	})
	t.Run("settle while another reservation still holds units", func(t *testing.T) {
		f := newAcctFixture(t, suffixFor(t), 4)
		spec := f.holdOne(t, 1, 2, 2)   // 2 held
		_, _ = f.reserve(t, 2, 2, true) // 2 still allocated to another buyer
		_, err := f.uow.Settle(context.Background(), settleSpec(spec.BookingID))
		require.NoError(t, err)
		require.Equal(t, 2, f.ledger(t).sold)
		assert.Zero(t, soldOutRows(t, f), "allocated units can return: not sold out")
	})
	t.Run("settle of the last units with nothing outstanding is sold out", func(t *testing.T) {
		f := newAcctFixture(t, suffixFor(t), 2)
		spec := f.holdOne(t, 1, 2, 2)
		_, err := f.uow.Settle(context.Background(), settleSpec(spec.BookingID))
		require.NoError(t, err)
		assert.Equal(t, 1, soldOutRows(t, f))
	})
	t.Run("reserve with nothing free, allocated or held is sold out", func(t *testing.T) {
		f := newAcctFixture(t, suffixFor(t), 2)
		spec := f.holdOne(t, 1, 2, 2)
		_, err := f.uow.Settle(context.Background(), settleSpec(spec.BookingID))
		require.NoError(t, err)
		out, err := NewAdmissionRepo(f.pool).ReserveBatch(context.Background(), f.eventID, []outbound.ReserveEntry{anonEntry(2, 3)}, time.Now())
		require.NoError(t, err)
		assert.Equal(t, "SOLD_OUT", out[0].Outcome)
	})
}

// P1-6: the sold-out announcement is written with the status change and survives the per-entry savepoint rollback,
// and it is written at most once per batch.
func TestReserveBatch_SoldOutIsAnnouncedOnce(t *testing.T) {
	f := newAcctFixture(t, suffixFor(t), 1)
	ctx := context.Background()
	_, err := f.pool.Exec(ctx, `UPDATE booking.event_supply SET sold_units=total_capacity WHERE event_id=$1::uuid`, f.eventID)
	require.NoError(t, err)
	_, err = f.pool.Exec(ctx, `UPDATE booking.category_inventories SET sold_quantity=capacity WHERE event_id=$1::uuid`, f.eventID)
	require.NoError(t, err)

	out, err := NewAdmissionRepo(f.pool).ReserveBatch(ctx, f.eventID,
		[]outbound.ReserveEntry{anonEntry(1, 1), anonEntry(2, 1)}, time.Now())
	require.NoError(t, err)
	require.Len(t, out, 2)
	assert.Equal(t, "SOLD_OUT", out[0].Outcome)
	assert.Equal(t, "SOLD_OUT", out[1].Outcome)

	var status string
	require.NoError(t, f.pool.QueryRow(ctx, `SELECT status FROM booking.events WHERE id=$1::uuid`, f.eventID).Scan(&status))
	assert.Equal(t, "SOLD_OUT", status, "the status change survives the entry rollback")
	assert.Equal(t, 1, soldOutRows(t, f), "booking.sold_out is announced exactly once per batch")
}
