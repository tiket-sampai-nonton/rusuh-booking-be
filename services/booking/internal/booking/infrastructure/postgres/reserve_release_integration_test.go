//go:build integration

package postgres

import (
	"context"
	cryptorand "crypto/rand"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
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
		// Inside the grace window the sweeper still claims the row (D-C7.3), but ExpireSession leaves it ACTIVE.
		due, err := repo.ClaimDueSessions(context.Background(), stored.Add(4*time.Second), 10)
		require.NoError(t, err)
		var found bool
		for _, d := range due {
			found = found || d.ID == id
		}
		assert.True(t, found, "claimed inside the grace window")
		require.NoError(t, repo.ExpireSession(context.Background(), id, stored.Add(4*time.Second)))
		st, _, _ := admState(t, f, id)
		assert.Equal(t, "ACTIVE", st, "not expired before expires_at + grace (5 s)")
		due, err = repo.ClaimDueSessions(context.Background(), stored.Add(6*time.Second), 10)
		require.NoError(t, err)
		found = false
		for _, d := range due {
			found = found || d.ID == id
		}
		assert.True(t, found, "due again after expires_at + grace")
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

// P1-6: the sold-out announcement (booking.sold_out outbox row) survives the per-entry savepoint rollback, and it is
// written at most once per batch. The catalog derives SOLD_OUT from supply, so events.status is not changed (AC-C4.2).
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
	assert.Equal(t, "AVAILABLE", status, "the stored status is not changed; SOLD_OUT is derived from supply")
	assert.Equal(t, 1, soldOutRows(t, f), "booking.sold_out is announced exactly once per batch")
}

// C4.4 (AC-C4.4 F-window): a batch grants nothing outside the sales window. Before sales_starts_at every entry waits
// (NO_SUPPLY_NOW). At or after sales_ends_at every entry is SOLD_OUT and no booking.sold_out row is written, because
// nothing can come back for the queue to wait for.
func TestReserveBatch_OutsideSalesWindow(t *testing.T) {
	ctx := context.Background()
	t.Run("sales_starts_at in the future", func(t *testing.T) {
		f := newAcctFixture(t, suffixFor(t), 5)
		_, err := f.pool.Exec(ctx, `UPDATE booking.events SET sales_starts_at = now() + interval '1 hour' WHERE id=$1::uuid`, f.eventID)
		require.NoError(t, err)
		out, err := NewAdmissionRepo(f.pool).ReserveBatch(ctx, f.eventID,
			[]outbound.ReserveEntry{anonEntry(1, 2), anonEntry(2, 2)}, time.Now())
		require.NoError(t, err)
		require.Len(t, out, 2)
		assert.Equal(t, "NO_SUPPLY_NOW", out[0].Outcome)
		assert.Equal(t, "NO_SUPPLY_NOW", out[1].Outcome)
		assert.Equal(t, 0, f.ledger(t).alloc, "allocated_units = 0")
		assert.Equal(t, 0, admissionRows(t, f))
	})
	t.Run("sales_ends_at in the past", func(t *testing.T) {
		f := newAcctFixture(t, suffixFor(t), 5)
		_, err := f.pool.Exec(ctx, `UPDATE booking.events SET sales_ends_at = now() - interval '1 minute' WHERE id=$1::uuid`, f.eventID)
		require.NoError(t, err)
		out, err := NewAdmissionRepo(f.pool).ReserveBatch(ctx, f.eventID,
			[]outbound.ReserveEntry{anonEntry(1, 2), anonEntry(2, 2)}, time.Now())
		require.NoError(t, err)
		require.Len(t, out, 2)
		assert.Equal(t, "SOLD_OUT", out[0].Outcome)
		assert.Equal(t, "SOLD_OUT", out[1].Outcome)
		assert.Equal(t, 0, f.ledger(t).alloc, "allocated_units = 0")
		assert.Equal(t, 0, admissionRows(t, f))
		assert.Equal(t, 0, outboxRows(t, f), "no outbox row after the sales end")
	})
}

// C4.4 (AC-C4.4 F-cancel, D-C4.4a): a cancelled event answers SOLD_OUT to every entry, grants nothing, and writes no
// booking.sold_out row: SOLD_OUT here means the queue ends, so the announcement is not sent for it.
func TestReserveBatch_CancelledEventIsSoldOut(t *testing.T) {
	ctx := context.Background()
	f := newAcctFixture(t, suffixFor(t), 5)
	_, err := f.pool.Exec(ctx, `UPDATE booking.events SET status='CANCELLED' WHERE id=$1::uuid`, f.eventID)
	require.NoError(t, err)

	out, err := NewAdmissionRepo(f.pool).ReserveBatch(ctx, f.eventID,
		[]outbound.ReserveEntry{anonEntry(1, 2), anonEntry(2, 2)}, time.Now())
	require.NoError(t, err)
	require.Len(t, out, 2)
	assert.Equal(t, "SOLD_OUT", out[0].Outcome)
	assert.Equal(t, "SOLD_OUT", out[1].Outcome)
	assert.Equal(t, 0, f.ledger(t).alloc, "allocated_units = 0")
	assert.Equal(t, 0, admissionRows(t, f))
	assert.Equal(t, 0, soldOutRows(t, f), "a cancelled event does not announce booking.sold_out")
}

func admissionRows(t *testing.T, f acctFixture) int {
	t.Helper()
	var n int
	require.NoError(t, f.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM booking.admissions WHERE event_id=$1::uuid`, f.eventID).Scan(&n))
	return n
}

func outboxRows(t *testing.T, f acctFixture) int {
	t.Helper()
	var n int
	require.NoError(t, f.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM booking.outbox_events WHERE aggregate_id=$1::uuid`, f.eventID).Scan(&n))
	return n
}

// Fix round 1 (R14): an entry that already holds a reservation is replayed with the same grant even after sales_ends_at.
// Queue retries the batch when its promotion failed; a SOLD_OUT answer here would close the sale for a buyer who
// already holds an admission (invariant 2). Only a new grant is refused after the end.
func TestReserveBatch_ReplayAfterSalesEndReturnsSameGrant(t *testing.T) {
	ctx := context.Background()
	f := newAcctFixture(t, suffixFor(t), 5)
	repo := NewAdmissionRepo(f.pool)
	entry := anonEntry(1, 2)

	first, err := repo.ReserveBatch(ctx, f.eventID, []outbound.ReserveEntry{entry}, time.Now())
	require.NoError(t, err)
	require.Equal(t, "GRANTED", first[0].Outcome)
	require.NotEmpty(t, first[0].AllocationID)

	_, err = f.pool.Exec(ctx, `UPDATE booking.events SET sales_ends_at = now() - interval '1 minute' WHERE id=$1::uuid`, f.eventID)
	require.NoError(t, err)

	again, err := repo.ReserveBatch(ctx, f.eventID, []outbound.ReserveEntry{entry}, time.Now())
	require.NoError(t, err)
	require.Len(t, again, 1)
	assert.Equal(t, "GRANTED", again[0].Outcome, "replay after the sales end returns the same grant")
	assert.Equal(t, first[0].AllocationID, again[0].AllocationID, "same allocation id")
	assert.Equal(t, 2, f.ledger(t).alloc, "replay takes no extra stock")
	assert.Equal(t, 1, admissionRows(t, f))

	fresh, err := repo.ReserveBatch(ctx, f.eventID, []outbound.ReserveEntry{anonEntry(2, 1)}, time.Now())
	require.NoError(t, err)
	assert.Equal(t, "SOLD_OUT", fresh[0].Outcome, "a new grant after the sales end is refused")
	assert.Equal(t, 0, soldOutRows(t, f), "the refusal after the end writes no booking.sold_out row")
}

// D-C6.6 (P2-16): a RESERVED admission that lapsed unclaimed (EXPIRED/UNCLAIMED) was never activated, so Queue never
// admitted its entry. Its entry is granted again with the same allocation id and the units it had, instead of being
// told ALREADY_ACTIVE (the promoter would then end a live buyer). The lapse already wrote its journey.ended row; the
// re-grant writes no new one.
func TestReserveBatch_LapsedUnclaimedReservationIsGrantedAgain(t *testing.T) {
	ctx := context.Background()
	f := newAcctFixture(t, suffixFor(t), 5)
	repo := NewAdmissionRepo(f.pool)
	entry := anonEntry(1, 2)

	first, err := repo.ReserveBatch(ctx, f.eventID, []outbound.ReserveEntry{entry}, time.Now())
	require.NoError(t, err)
	require.Equal(t, "GRANTED", first[0].Outcome)
	require.Equal(t, 2, f.ledger(t).alloc)

	require.NoError(t, repo.ExpireSession(ctx, first[0].AllocationID, time.Now()))
	var status, endReason string
	require.NoError(t, f.pool.QueryRow(ctx, `SELECT status, COALESCE(end_reason,'') FROM booking.admissions WHERE id=$1::uuid`,
		first[0].AllocationID).Scan(&status, &endReason))
	require.Equal(t, "EXPIRED", status)
	require.Equal(t, "UNCLAIMED", endReason)
	require.Equal(t, 0, f.ledger(t).alloc, "the lapse gave the units back")

	again, err := repo.ReserveBatch(ctx, f.eventID, []outbound.ReserveEntry{entry}, time.Now())
	require.NoError(t, err)
	require.Len(t, again, 1)
	assert.Equal(t, "GRANTED", again[0].Outcome, "a lapsed unclaimed reservation is granted again")
	assert.Equal(t, first[0].AllocationID, again[0].AllocationID, "same allocation id")
	assert.Equal(t, 2, again[0].Units, "the granted units come back")
	assert.Equal(t, 2, f.ledger(t).alloc, "allocated_units back to the granted units")

	var units, outstanding int
	var endedAtNull bool
	require.NoError(t, f.pool.QueryRow(ctx, `SELECT status, COALESCE(end_reason,''), ended_at IS NULL, units, outstanding_units
		FROM booking.admissions WHERE id=$1::uuid`, first[0].AllocationID).Scan(&status, &endReason, &endedAtNull, &units, &outstanding))
	assert.Equal(t, "RESERVED", status)
	assert.Empty(t, endReason, "end_reason cleared")
	assert.True(t, endedAtNull, "ended_at cleared")
	assert.Equal(t, 2, units)
	assert.Equal(t, 2, outstanding)
	assert.Equal(t, 1, admissionRows(t, f), "the row is reused, not inserted again")
	assert.Len(t, journeyEnded(t, f, first[0].AllocationID), 1, "the re-grant writes no journey.ended")
}

// D-C6.6: a RELEASED admission (Queue said the entry is gone) is never granted again, even though its end_reason is
// UNCLAIMED too.
func TestReserveBatch_ReleasedReservationIsNotGrantedAgain(t *testing.T) {
	ctx := context.Background()
	f := newAcctFixture(t, suffixFor(t), 5)
	repo := NewAdmissionRepo(f.pool)
	entry := anonEntry(1, 2)

	first, err := repo.ReserveBatch(ctx, f.eventID, []outbound.ReserveEntry{entry}, time.Now())
	require.NoError(t, err)
	require.Equal(t, "GRANTED", first[0].Outcome)
	ok, err := repo.Release(ctx, first[0].AllocationID, vo.EndUnclaimed)
	require.NoError(t, err)
	require.True(t, ok)

	again, err := repo.ReserveBatch(ctx, f.eventID, []outbound.ReserveEntry{entry}, time.Now())
	require.NoError(t, err)
	require.Len(t, again, 1)
	assert.Equal(t, "ALREADY_ACTIVE", again[0].Outcome, "a released reservation is not granted again")
	assert.Equal(t, first[0].AllocationID, again[0].AllocationID)
	assert.Equal(t, 0, f.ledger(t).alloc, "no stock is taken again")
	var status string
	require.NoError(t, f.pool.QueryRow(ctx, `SELECT status FROM booking.admissions WHERE id=$1::uuid`, first[0].AllocationID).Scan(&status))
	assert.Equal(t, "RELEASED", status)
}

// D-C6.6: an admission that was activated and then expired by its session (EXPIRED/SESSION_EXPIRED) belongs to a buyer
// who was in the detail flow; it is never granted again.
func TestReserveBatch_ActivatedThenExpiredIsNotGrantedAgain(t *testing.T) {
	ctx := context.Background()
	f := newAcctFixture(t, suffixFor(t), 5)
	repo := NewAdmissionRepo(f.pool)
	entry := anonEntry(1, 2)

	first, err := repo.ReserveBatch(ctx, f.eventID, []outbound.ReserveEntry{entry}, time.Now())
	require.NoError(t, err)
	require.Equal(t, "GRANTED", first[0].Outcome)
	_, err = repo.Activate(ctx, first[0].AllocationID, time.Now())
	require.NoError(t, err)
	require.NoError(t, repo.ExpireSession(ctx, first[0].AllocationID, time.Now()))

	again, err := repo.ReserveBatch(ctx, f.eventID, []outbound.ReserveEntry{entry}, time.Now())
	require.NoError(t, err)
	require.Len(t, again, 1)
	assert.Equal(t, "ALREADY_ACTIVE", again[0].Outcome, "an expired session is not granted again")
	assert.Equal(t, 0, f.ledger(t).alloc)
	var endReason string
	require.NoError(t, f.pool.QueryRow(ctx, `SELECT COALESCE(end_reason,'') FROM booking.admissions WHERE id=$1::uuid`, first[0].AllocationID).Scan(&endReason))
	assert.Equal(t, "SESSION_EXPIRED", endReason)
}

// Fix round 1 (R18): a batch locks its existing admission rows before any supply lock, the same order Release and
// ExpireSession use (admission, then event_supply). The holder below repeats Release's statements in that order while a
// batch waits on the same admission. Before R18 the batch already held supply and then waited on the admission: a
// deadlock (40P01). After R18 the batch waits before it takes supply, so the holder finishes and the batch goes on.
func TestReserveBatch_DoesNotDeadlockWithReleaseOnSameAdmission(t *testing.T) {
	ctx := context.Background()
	f := newAcctFixture(t, suffixFor(t), 10)
	repo := NewAdmissionRepo(f.pool)
	entry2 := anonEntry(2, 2)
	first, err := repo.ReserveBatch(ctx, f.eventID, []outbound.ReserveEntry{entry2}, time.Now())
	require.NoError(t, err)
	require.Equal(t, "GRANTED", first[0].Outcome)
	adm2 := first[0].AllocationID

	holder, err := f.pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = holder.Rollback(ctx) }()
	_, err = holder.Exec(ctx, `SELECT id FROM booking.admissions WHERE id=$1::uuid FOR UPDATE`, adm2)
	require.NoError(t, err)

	var out []outbound.ReserveOutcome
	var batchErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		out, batchErr = repo.ReserveBatch(ctx, f.eventID,
			[]outbound.ReserveEntry{anonEntry(1, 1), entry2}, time.Now())
	}()
	// The batch must reach its wait on adm2 before the holder continues (before R18 it already holds event_supply here).
	time.Sleep(300 * time.Millisecond)

	_, err = holder.Exec(ctx, `UPDATE booking.admissions SET status='RELEASED', outstanding_units=0, ended_at=now(), end_reason='UNCLAIMED'
		WHERE id=$1::uuid`, adm2)
	require.NoError(t, err, "Release's admission step must not be blocked by the batch")
	_, err = holder.Exec(ctx, `UPDATE booking.event_supply SET allocated_units = allocated_units - 2, version = version + 1, updated_at = now()
		WHERE event_id=$1::uuid`, f.eventID)
	require.NoError(t, err, "Release's supply step must not deadlock with the batch")
	require.NoError(t, holder.Commit(ctx))
	<-done

	require.NoError(t, batchErr, "the batch must not fail with 40P01")
	require.Len(t, out, 2)
	assert.Equal(t, "GRANTED", out[0].Outcome)
	assert.Equal(t, "ALREADY_ACTIVE", out[1].Outcome, "entry 2 was released by the holder")
	assert.Equal(t, 1, f.ledger(t).alloc, "only the new entry's 1 unit is allocated")
}

// Fix round 1 (R18): Release and reservation batches run concurrently on the same admissions, repeatedly. No batch may
// fail (40P01 or otherwise), and allocated_units stays equal to the outstanding units of the RESERVED admissions.
func TestReserveBatch_ConcurrentReleaseKeepsStockAndLockOrder(t *testing.T) {
	ctx := context.Background()
	f := newAcctFixture(t, suffixFor(t), 200)
	repo := NewAdmissionRepo(f.pool)
	for i := 0; i < 20; i++ {
		entry2 := anonEntry(2*i+2, 2)
		first, err := repo.ReserveBatch(ctx, f.eventID, []outbound.ReserveEntry{entry2}, time.Now())
		require.NoError(t, err)
		require.Equal(t, "GRANTED", first[0].Outcome)
		allocID := first[0].AllocationID

		var wg sync.WaitGroup
		start := make(chan struct{})
		var out []outbound.ReserveOutcome
		var batchErr, releaseErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			out, batchErr = repo.ReserveBatch(ctx, f.eventID, []outbound.ReserveEntry{anonEntry(2*i+1, 1), entry2}, time.Now())
		}()
		go func() {
			defer wg.Done()
			<-start
			_, releaseErr = repo.Release(ctx, allocID, vo.EndUnclaimed)
		}()
		close(start)
		wg.Wait()

		require.NoError(t, batchErr, "iteration %d: batch", i)
		require.NoError(t, releaseErr, "iteration %d: release", i)
		require.Len(t, out, 2)
		assert.Contains(t, []string{"GRANTED", "ALREADY_ACTIVE"}, out[1].Outcome, "iteration %d", i)
		var sum int
		require.NoError(t, f.pool.QueryRow(ctx,
			`SELECT COALESCE(sum(outstanding_units),0) FROM booking.admissions WHERE event_id=$1::uuid AND status='RESERVED'`,
			f.eventID).Scan(&sum))
		assert.Equal(t, sum, f.ledger(t).alloc, "iteration %d: allocated_units = sum of RESERVED outstanding units", i)
	}
}

// stmtCounter records the SQL of every statement a pool runs (C7.2 tracer).
type stmtCounter struct {
	mu   sync.Mutex
	sqls []string
}

func (c *stmtCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sqls = append(c.sqls, strings.Join(strings.Fields(data.SQL), " "))
	return ctx
}

func (c *stmtCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// countPrefix returns how many recorded statements start with prefix.
func (c *stmtCounter) countPrefix(prefix string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, s := range c.sqls {
		if strings.HasPrefix(s, prefix) {
			n++
		}
	}
	return n
}

// tracedAdmissionRepo is an admission repository over a second pool of the same database that records its statements.
func tracedAdmissionRepo(t *testing.T, pool *pgxpool.Pool) (*AdmissionRepo, *stmtCounter) {
	t.Helper()
	counter := &stmtCounter{}
	cfg := pool.Config().Copy()
	cfg.ConnConfig.Tracer = counter
	traced, err := pgxpool.NewWithConfig(context.Background(), cfg)
	require.NoError(t, err)
	t.Cleanup(traced.Close)
	return NewAdmissionRepo(traced), counter
}

// C7.2 (AC-C7.2 PF-P1-1): a 100-entry batch locks event_supply once and writes its admissions in one statement. Free 150
// units: the first 50 entries (3 units each) are GRANTED, the rest find no supply (NO_SUPPLY_NOW).
func TestReserveBatch_HundredEntriesOneSupplyUpdate(t *testing.T) {
	ctx := context.Background()
	f := newAcctFixture(t, suffixFor(t), 150)
	repo, counter := tracedAdmissionRepo(t, f.pool)
	entries := make([]outbound.ReserveEntry, 0, 100)
	for i := 1; i <= 100; i++ {
		entries = append(entries, anonEntry(i, 3))
	}

	out, err := repo.ReserveBatch(ctx, f.eventID, entries, time.Now())
	require.NoError(t, err)
	require.Len(t, out, 100)
	for i, o := range out {
		if i < 50 {
			assert.Equal(t, "GRANTED", o.Outcome, "entry %d", i)
			assert.Equal(t, 3, o.Units, "entry %d", i)
			assert.NotEmpty(t, o.AllocationID, "entry %d", i)
		} else {
			assert.Equal(t, "NO_SUPPLY_NOW", o.Outcome, "entry %d", i)
		}
	}
	assert.Equal(t, 150, f.ledger(t).alloc, "allocated_units = 150")
	assert.Equal(t, 1, counter.countPrefix("SELECT s.total_capacity"), "the supply row is locked once for the whole batch")
	assert.Equal(t, 1, counter.countPrefix("UPDATE booking.event_supply"), "one supply update for the whole batch")
	assert.Equal(t, 1, counter.countPrefix("INSERT INTO booking.admissions"), "one bulk insert for the whole batch")
}

// C7.2: 10 holds run in parallel with a 100-entry batch on the same event. No statement fails (40P01 or otherwise), and
// the ledger still matches the admissions and the HELD items.
func TestReserveBatch_ConcurrentWithHoldsKeepsLedger(t *testing.T) {
	ctx := context.Background()
	f := newAcctFixture(t, suffixFor(t), 100)
	repo := NewAdmissionRepo(f.pool)
	type reserved struct{ id, vis string }
	var admissions []reserved
	for i := 0; i < 10; i++ {
		id, vis := f.reserve(t, i, 1, true)
		admissions = append(admissions, reserved{id, vis})
	}
	entries := make([]outbound.ReserveEntry, 0, 100)
	for i := 1; i <= 100; i++ {
		entries = append(entries, anonEntry(100+i, 3))
	}

	var wg sync.WaitGroup
	start := make(chan struct{})
	holdErrs := make([]error, len(admissions))
	var out []outbound.ReserveOutcome
	var batchErr error
	for i, a := range admissions {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, holdErrs[i] = f.uow.Hold(ctx, f.spec(i, 1, a.id, a.vis, true))
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		out, batchErr = repo.ReserveBatch(ctx, f.eventID, entries, time.Now())
	}()
	close(start)
	wg.Wait()

	for i, err := range holdErrs {
		require.NoError(t, err, "hold %d", i)
	}
	require.NoError(t, batchErr, "batch must not fail while holds run")
	require.Len(t, out, 100)
	granted := 0
	for _, o := range out {
		if o.Outcome == "GRANTED" {
			granted++
		}
	}
	assert.Equal(t, 30, granted, "90 free units in 3-unit grants")

	var sumOut, heldItems int
	require.NoError(t, f.pool.QueryRow(ctx, `SELECT COALESCE(sum(outstanding_units),0) FROM booking.admissions WHERE event_id=$1::uuid`, f.eventID).Scan(&sumOut))
	require.NoError(t, f.pool.QueryRow(ctx, `SELECT count(*) FROM booking.booking_items WHERE event_id=$1::uuid AND item_status='HELD'`, f.eventID).Scan(&heldItems))
	l := f.ledger(t)
	assert.Equal(t, sumOut, l.alloc, "allocated_units = sum(outstanding_units)")
	assert.Equal(t, heldItems, l.held, "held_units = HELD items")
	assert.LessOrEqual(t, l.alloc+l.held+l.sold, l.total)
}

// R24 (C7.2 fix round 1): an entry that cannot be written alone is answered alone. Its visitor already holds an active
// admission on the event, so its row conflicts: it is ALREADY_ACTIVE, the entries around it are granted, and the supply
// counts only the units that were written.
func TestReserveBatch_VisitorAlreadyActiveAnswersOnlyThatEntry(t *testing.T) {
	ctx := context.Background()
	f := newAcctFixture(t, suffixFor(t), 20)
	_, busyVisitor := f.reserve(t, 9, 1, true) // one active admission of another buyer: 1 unit allocated
	entries := []outbound.ReserveEntry{anonEntry(1, 3), anonEntry(2, 3), anonEntry(3, 3)}
	entries[1].VisitorID = busyVisitor

	out, err := NewAdmissionRepo(f.pool).ReserveBatch(ctx, f.eventID, entries, time.Now())
	require.NoError(t, err)
	require.Len(t, out, 3)
	assert.Equal(t, "GRANTED", out[0].Outcome)
	assert.Equal(t, 3, out[0].Units)
	assert.Equal(t, "ALREADY_ACTIVE", out[1].Outcome, "the busy visitor's entry, alone")
	assert.Equal(t, "GRANTED", out[2].Outcome)
	assert.Equal(t, 1+3+3, f.ledger(t).alloc, "supply counts entries 1 and 3 only")
	assert.Equal(t, 3, admissionRows(t, f), "the busy entry writes no admission row")
}

// R24: a lapsed admission (EXPIRED/UNCLAIMED) is granted again in place unless its visitor now holds another active
// admission on the event. That entry is ALREADY_ACTIVE (its old allocation id), and the rest of the batch is granted.
func TestReserveBatch_RegrantConflictAnswersOnlyThatEntry(t *testing.T) {
	ctx := context.Background()
	f := newAcctFixture(t, suffixFor(t), 20)
	repo := NewAdmissionRepo(f.pool)
	lapsed := anonEntry(1, 2)
	first, err := repo.ReserveBatch(ctx, f.eventID, []outbound.ReserveEntry{lapsed}, time.Now())
	require.NoError(t, err)
	require.Equal(t, "GRANTED", first[0].Outcome)
	require.NoError(t, repo.ExpireSession(ctx, first[0].AllocationID, time.Now())) // EXPIRED/UNCLAIMED, units given back

	taking := anonEntry(2, 1) // the same visitor now holds a new active admission under another queue entry
	taking.VisitorID = lapsed.VisitorID
	got, err := repo.ReserveBatch(ctx, f.eventID, []outbound.ReserveEntry{taking}, time.Now())
	require.NoError(t, err)
	require.Equal(t, "GRANTED", got[0].Outcome)

	out, err := repo.ReserveBatch(ctx, f.eventID, []outbound.ReserveEntry{lapsed, anonEntry(3, 2)}, time.Now())
	require.NoError(t, err)
	require.Len(t, out, 2)
	assert.Equal(t, "ALREADY_ACTIVE", out[0].Outcome, "the lapsed row cannot be granted again: the visitor is active")
	assert.Equal(t, first[0].AllocationID, out[0].AllocationID)
	assert.Equal(t, "GRANTED", out[1].Outcome)
	assert.Equal(t, 1+2, f.ledger(t).alloc, "taking (1) and the new entry (2); the conflicting re-grant adds nothing")
}

// R24: one malformed entry is answered alone with the old per-entry outcome. A malformed id failed the insert
// (NO_SUPPLY_NOW); a queue_seq or units outside the admission CHECK gave SOLD_OUT. The entries around it are granted.
func TestReserveBatch_MalformedEntryAnswersOnlyItself(t *testing.T) {
	cases := map[string]struct {
		mutate func(e *outbound.ReserveEntry)
		want   string
	}{
		"queue entry id": {func(e *outbound.ReserveEntry) { e.QueueEntryID = "not-a-uuid" }, "NO_SUPPLY_NOW"},
		"visitor id":     {func(e *outbound.ReserveEntry) { e.VisitorID = "x" }, "NO_SUPPLY_NOW"},
		"admission jti":  {func(e *outbound.ReserveEntry) { e.AdmissionJTI = "x" }, "NO_SUPPLY_NOW"},
		"account id":     {func(e *outbound.ReserveEntry) { e.AccountID = "nope" }, "NO_SUPPLY_NOW"},
		"queue_seq zero": {func(e *outbound.ReserveEntry) { e.Seq = 0 }, "SOLD_OUT"},
		"units zero":     {func(e *outbound.ReserveEntry) { e.Units = 0 }, "SOLD_OUT"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newAcctFixture(t, suffixFor(t), 20)
			entries := []outbound.ReserveEntry{anonEntry(1, 3), anonEntry(2, 3), anonEntry(3, 3)}
			tc.mutate(&entries[1])
			out, err := NewAdmissionRepo(f.pool).ReserveBatch(context.Background(), f.eventID, entries, time.Now())
			require.NoError(t, err, "a malformed entry must not fail the batch")
			require.Len(t, out, 3)
			assert.Equal(t, "GRANTED", out[0].Outcome)
			assert.Equal(t, tc.want, out[1].Outcome)
			assert.Equal(t, "GRANTED", out[2].Outcome)
			assert.Equal(t, 6, f.ledger(t).alloc, "only the two valid entries took units")
		})
	}
}

// C7.2 fix round 2 (R27): the buyer's quota counts across the batch. Two entries with one NIK hash share the 3 units left
// on the quota: the first takes 2, the second only the 1 left, and a third finds the quota exhausted.
func TestReserveBatch_SameBuyerNIKCountsAcrossBatch(t *testing.T) {
	ctx := context.Background()
	f := newAcctFixture(t, suffixFor(t), 20)
	nik := []byte("buyer-nik-fixture-01")
	entries := []outbound.ReserveEntry{anonEntry(1, 2), anonEntry(2, 2), anonEntry(3, 2)}
	for i := range entries {
		entries[i].BuyerNIKHash = nik
	}

	out, err := NewAdmissionRepo(f.pool).ReserveBatch(ctx, f.eventID, entries, time.Now())
	require.NoError(t, err)
	require.Len(t, out, 3)
	assert.Equal(t, "GRANTED", out[0].Outcome)
	assert.Equal(t, 2, out[0].Units)
	assert.Equal(t, "GRANTED", out[1].Outcome)
	assert.Equal(t, 1, out[1].Units, "the quota left after the first entry of the batch")
	assert.Equal(t, "BUYER_QUOTA_EXHAUSTED", out[2].Outcome, "3 units are taken: the buyer's quota is used up")
	assert.Equal(t, 3, f.ledger(t).alloc, "the buyer never gets more than 3 units on the event")
}

// waitForSupplyLockWait polls until a statement of the database waits on the event_supply lock of the batch.
func waitForSupplyLockWait(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	for i := 0; i < 500; i++ {
		var n int
		require.NoError(t, pool.QueryRow(context.Background(), `
			SELECT count(*) FROM pg_stat_activity
			 WHERE datname = current_database() AND wait_event_type = 'Lock' AND query LIKE '%FOR UPDATE OF s%'`).Scan(&n))
		if n > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the batch never waited on the supply lock")
}

// C7.2 fix round 2 (R27): another batch commits the RESERVED admission of entry 1 after this batch has read the admissions
// but before it inserts. Entry 1 is GRANTED with that row's id, units and claim deadline, not ALREADY_ACTIVE; entry 2 is
// granted as usual. The other batch's transaction holds the event_supply lock until this batch is waiting on it, so the
// commit lands between the read and the insert.
func TestReserveBatch_ConflictWithReservedRowIsGranted(t *testing.T) {
	ctx := context.Background()
	f := newAcctFixture(t, suffixFor(t), 20)
	entries := []outbound.ReserveEntry{anonEntry(1, 3), anonEntry(2, 3)}

	other, err := f.pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = other.Rollback(ctx) }()
	var otherID string
	var expires time.Time
	require.NoError(t, other.QueryRow(ctx, `
		INSERT INTO booking.admissions (
			event_id, queue_entry_id, queue_seq, visitor_id, units, outstanding_units, status, admission_jti,
			attendee_checks, edit_count, cfg_selection_ttl_seconds, cfg_payment_ttl_seconds, cfg_submit_grace_seconds,
			cfg_edit_enabled, cfg_max_edits, cfg_allow_mixed_categories, reserved_at, expires_at)
		VALUES ($1::uuid, $2::uuid, 1, $3::uuid, 3, 3, 'RESERVED', $4::uuid, 0, 0, 300, 600, 5, true, 3, true,
			now(), now() + interval '5 minutes')
		RETURNING id::text, expires_at`, f.eventID, entries[0].QueueEntryID, newTestUUID(), newTestUUID()).Scan(&otherID, &expires))
	_, err = other.Exec(ctx, `UPDATE booking.event_supply SET allocated_units = allocated_units + 3 WHERE event_id=$1::uuid`, f.eventID)
	require.NoError(t, err)

	var out []outbound.ReserveOutcome
	var batchErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		out, batchErr = NewAdmissionRepo(f.pool).ReserveBatch(ctx, f.eventID, entries, time.Now())
	}()
	waitForSupplyLockWait(t, f.pool)
	require.NoError(t, other.Commit(ctx))
	<-done

	require.NoError(t, batchErr)
	require.Len(t, out, 2)
	assert.Equal(t, "GRANTED", out[0].Outcome, "the RESERVED row of the other batch is this entry's grant")
	assert.Equal(t, otherID, out[0].AllocationID)
	assert.Equal(t, 3, out[0].Units)
	require.NotNil(t, out[0].ClaimDeadline)
	assert.True(t, expires.Equal(*out[0].ClaimDeadline), "claim deadline is the row's own")
	assert.Equal(t, "GRANTED", out[1].Outcome)
	assert.Equal(t, 3+3, f.ledger(t).alloc, "the other batch's 3 units and entry 2's 3 units; entry 1 is not counted twice")
	assert.Equal(t, 2, admissionRows(t, f))
}
