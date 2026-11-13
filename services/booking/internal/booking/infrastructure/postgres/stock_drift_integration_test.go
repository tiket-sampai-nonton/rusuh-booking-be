//go:build integration

package postgres

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	vo "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/valueobject"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

// seedDriftEvent inserts an on-sale event with one category per capacity and supply equal to their sum. Allocation and
// holds start at zero, so the seeded event has no drift.
func seedDriftEvent(t *testing.T, pool *pgxpool.Pool, suffix string, capacities ...int) (string, []string) {
	t.Helper()
	ctx := context.Background()
	eventID := fmt.Sprintf("d0000000-0000-0000-0000-%s", suffix)
	_, err := pool.Exec(ctx, `
		INSERT INTO booking.events
			(id, slug, name, venue, starts_at, sales_starts_at, sales_ends_at, status,
			 max_tickets_per_order, max_tickets_per_buyer_nik, selection_ttl_seconds,
			 payment_ttl_seconds, claim_window_seconds, max_attendee_checks_per_session, published_at)
		VALUES ($1::uuid, $2, $2, 'Venue', now()+interval '1 hour', now()-interval '1 hour',
			now()+interval '2 hours', 'AVAILABLE', 3, 3, 300, 600, 180, 10, now())`, eventID, "slug-"+eventID)
	require.NoError(t, err)

	cats := make([]string, len(capacities))
	total := 0
	for i, capacity := range capacities {
		cats[i] = fmt.Sprintf("d0000000-0000-0001-%04d-%s", i, suffix)
		_, err = pool.Exec(ctx, `
			INSERT INTO booking.ticket_categories (id, event_id, code, name, price_minor, currency, display_order)
			VALUES ($1::uuid, $2::uuid, $3, 'Category', 10000, 'IDR', $4)`, cats[i], eventID, fmt.Sprintf("C%d", i), i)
		require.NoError(t, err)
		_, err = pool.Exec(ctx, `
			INSERT INTO booking.category_inventories (category_id, event_id, capacity)
			VALUES ($1::uuid, $2::uuid, $3)`, cats[i], eventID, capacity)
		require.NoError(t, err)
		total += capacity
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO booking.event_supply (event_id, total_capacity, allocated_units) VALUES ($1::uuid, $2, 0)`, eventID, total)
	require.NoError(t, err)
	return eventID, cats
}

func TestStockDrift_ReportsInjectedDrift(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	eventID, cats := seedDriftEvent(t, pool, suffixFor(t), 10, 10)
	drift := NewDriftStats(pool)

	rows, err := drift.StockDrift(ctx)
	require.NoError(t, err)
	require.Empty(t, rows, "a freshly seeded event has no drift")

	_, err = pool.Exec(ctx, `UPDATE booking.event_supply SET held_units = held_units + 1 WHERE event_id=$1::uuid`, eventID)
	require.NoError(t, err)
	rows, err = drift.StockDrift(ctx)
	require.NoError(t, err)
	assert.Equal(t, []outbound.StockDrift{{EventID: eventID, Kind: "held", Delta: 1}}, rows)
	_, err = pool.Exec(ctx, `UPDATE booking.event_supply SET held_units = held_units - 1 WHERE event_id=$1::uuid`, eventID)
	require.NoError(t, err)

	// Each remaining kind is injected on its own, reported with its own delta, and removed again.
	cases := []struct {
		kind, inject, undo string
		injectArgs         []any
		undoArgs           []any
		delta              int
	}{
		{"sold", `UPDATE booking.event_supply SET sold_units = sold_units + 2 WHERE event_id=$1::uuid`,
			`UPDATE booking.event_supply SET sold_units = sold_units - 2 WHERE event_id=$1::uuid`,
			[]any{eventID}, []any{eventID}, 2},
		{"allocated", `UPDATE booking.event_supply SET allocated_units = allocated_units + 3 WHERE event_id=$1::uuid`,
			`UPDATE booking.event_supply SET allocated_units = allocated_units - 3 WHERE event_id=$1::uuid`,
			[]any{eventID}, []any{eventID}, 3},
		{"category_held", `UPDATE booking.category_inventories SET held_quantity = held_quantity + 1 WHERE category_id=$1::uuid`,
			`UPDATE booking.category_inventories SET held_quantity = held_quantity - 1 WHERE category_id=$1::uuid`,
			[]any{cats[0]}, []any{cats[0]}, 1},
		{"category_sold", `UPDATE booking.category_inventories SET sold_quantity = sold_quantity + 2 WHERE category_id=$1::uuid`,
			`UPDATE booking.category_inventories SET sold_quantity = sold_quantity - 2 WHERE category_id=$1::uuid`,
			[]any{cats[1]}, []any{cats[1]}, 2},
		{"quota_held", `INSERT INTO booking.buyer_quotas (event_id, buyer_nik_hash, held_quantity) VALUES ($1::uuid, $2, 1)`,
			`DELETE FROM booking.buyer_quotas WHERE event_id=$1::uuid AND buyer_nik_hash=$2`,
			[]any{eventID, []byte("quota-drift")}, []any{eventID, []byte("quota-drift")}, 1},
	}
	for _, c := range cases {
		t.Run(c.kind, func(t *testing.T) {
			_, err := pool.Exec(ctx, c.inject, c.injectArgs...)
			require.NoError(t, err)
			rows, err := drift.StockDrift(ctx)
			require.NoError(t, err)
			assert.Equal(t, []outbound.StockDrift{{EventID: eventID, Kind: c.kind, Delta: c.delta}}, rows)
			_, err = pool.Exec(ctx, c.undo, c.undoArgs...)
			require.NoError(t, err)
			rows, err = drift.StockDrift(ctx)
			require.NoError(t, err)
			assert.Empty(t, rows)
		})
	}
}

// driftEntry is one queue entry of the property test: its admission is created by ReserveBatch.
type driftEntry struct {
	qid, visitor, jti string
	seq               int64
	edit              bool
}

// driftDriver picks the random steps. Every random draw happens on the driver goroutine. The step functions read their
// target from the live state (the nth matching row, ordered by id, modulo the count), so a step reaches the states the
// lifecycle actually produces. Only the choice of kind and the nth row come from the seed; the interleaving varies.
type driftDriver struct {
	ctx     context.Context
	pool    *pgxpool.Pool
	uow     *UnitOfWork
	adms    *AdmissionRepo
	eventID string
	cats    []string
	rng     *rand.Rand
	entries []driftEntry
	refs    int
	holders int
}

// Kinds of the random steps (see pick).
const (
	opReserve = iota
	opActivate
	opHold
	opEdit
	opPaymentLock
	opSettlePaid
	opSettleLate
	opSettleMismatch
	opExpireTimeout
	opExpireCancelled
	opExpireReview
	opRelease
	opExpireSession
	opSweep
)

var driftWeights = [...]int{3, 2, 4, 2, 1, 3, 1, 1, 2, 1, 1, 2, 1, 1}

func (d *driftDriver) pick() func() error {
	total := 0
	for _, w := range driftWeights {
		total += w
	}
	n := d.rng.Intn(total)
	kind := 0
	for n >= driftWeights[kind] {
		n -= driftWeights[kind]
		kind++
	}
	nth := d.rng.Intn(1 << 16)
	switch kind {
	case opReserve:
		return d.reserve()
	case opActivate:
		return d.admissionCall("status='RESERVED'", nth, func(id string) error {
			_, err := d.adms.Activate(d.ctx, id, time.Now())
			return ignoreDomain(err)
		})
	case opHold:
		return d.hold(nth)
	case opEdit:
		return d.bookingEdit(nth)
	case opPaymentLock:
		return d.bookingCall(nth, func(id string, _ bookingState) error {
			_, err := d.uow.PaymentLock(d.ctx, outbound.PaymentLockSpec{BookingID: id, AttemptKey: "drift", CorrelationID: "drift"})
			return ignoreDomain(err)
		})
	case opSettlePaid, opSettleLate, opSettleMismatch:
		return d.bookingCall(nth, func(id string, b bookingState) error {
			spec := settleSpec(id)
			spec.AmountMinor, spec.SettlementTime = b.amount, time.Now()
			switch kind {
			case opSettleLate:
				spec.SettlementTime = b.expires.Add(time.Minute)
			case opSettleMismatch:
				spec.AmountMinor = b.amount + 1000
			}
			_, err := d.uow.Settle(d.ctx, spec)
			return ignoreDomain(err)
		})
	case opExpireTimeout:
		return d.bookingCall(nth, func(id string, _ bookingState) error {
			_, err := d.uow.Expire(d.ctx, outbound.ExpireSpec{BookingID: id, Reason: "PAYMENT_TIMEOUT", CorrelationID: "drift"})
			return ignoreDomain(err)
		})
	case opExpireCancelled:
		return d.bookingCall(nth, func(id string, _ bookingState) error {
			_, err := d.uow.Expire(d.ctx, outbound.ExpireSpec{BookingID: id, Reason: "CANCELLED", CorrelationID: "drift"})
			return ignoreDomain(err)
		})
	case opExpireReview:
		return d.bookingCall(nth, func(id string, _ bookingState) error {
			_, err := d.uow.Expire(d.ctx, outbound.ExpireSpec{BookingID: id, Reason: "PAYMENT_TIMEOUT", Review: true, CorrelationID: "drift"})
			return ignoreDomain(err)
		})
	case opRelease:
		reason := vo.EndUnclaimed
		if d.rng.Intn(2) == 0 {
			reason = vo.EndLeftWithoutBuying
		}
		return d.admissionCall("status IN ('RESERVED','ACTIVE','EDITABLE')", nth, func(id string) error {
			_, err := d.adms.Release(d.ctx, id, reason)
			return ignoreDomain(err)
		})
	case opExpireSession:
		return d.admissionCall("status IN ('RESERVED','ACTIVE','EDITABLE')", nth, func(id string) error {
			return d.adms.ExpireSession(d.ctx, id, time.Now().Add(time.Hour))
		})
	default: // opSweep: the session sweeper claims two due sessions and expires them
		return func() error {
			due, err := d.adms.ClaimDueSessions(d.ctx, time.Now().Add(time.Hour), 2)
			if err != nil {
				return err
			}
			for _, a := range due {
				if err := d.adms.ExpireSession(d.ctx, a.ID, time.Now().Add(time.Hour)); err != nil {
					return err
				}
			}
			return nil
		}
	}
}

// ignoreDomain drops the typed business refusals a random step is expected to meet (a stale state, a limit, a lock).
// Database failures, deadlocks included, are returned so the test fails on them.
func ignoreDomain(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return err
	}
	return nil
}

// rowAt reads the nth row (modulo the count, ordered by id) of table where cond, and scans cols into dest. found is
// false when no row matches.
func (d *driftDriver) rowAt(table, cols, cond string, nth int, dest ...any) (bool, error) {
	var n int
	if err := d.pool.QueryRow(d.ctx, "SELECT count(*) FROM "+table+" WHERE "+cond).Scan(&n); err != nil {
		return false, err
	}
	if n == 0 {
		return false, nil
	}
	err := d.pool.QueryRow(d.ctx, "SELECT "+cols+" FROM "+table+" WHERE "+cond+" ORDER BY id OFFSET $1 LIMIT 1", nth%n).Scan(dest...)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// reserve grants or replays up to three queue entries in one batch. Some batches carry a malformed entry or repeat one,
// so the per-entry answers of C7.2 run too. A lapsed UNCLAIMED entry in the batch is granted again (C6.6).
func (d *driftDriver) reserve() func() error {
	n := 1 + d.rng.Intn(3)
	batch := make([]outbound.ReserveEntry, 0, n+1)
	for i := 0; i < n; i++ {
		e := d.entries[d.rng.Intn(len(d.entries))]
		re := outbound.ReserveEntry{QueueEntryID: e.qid, Seq: e.seq, VisitorID: e.visitor, AdmissionJTI: e.jti, Units: 1 + d.rng.Intn(3)}
		if d.rng.Intn(8) == 0 {
			re.VisitorID = "not-a-uuid"
		}
		batch = append(batch, re)
	}
	if d.rng.Intn(4) == 0 {
		batch = append(batch, batch[0])
	}
	return func() error {
		_, err := d.adms.ReserveBatch(d.ctx, d.eventID, batch, time.Now())
		return err
	}
}

// admissionCall runs fn with the nth admission matching cond, or does nothing when none matches.
func (d *driftDriver) admissionCall(cond string, nth int, fn func(admissionID string) error) func() error {
	return func() error {
		var id string
		found, err := d.rowAt("booking.admissions", "id::text", cond, nth, &id)
		if err != nil || !found {
			return err
		}
		return fn(id)
	}
}

// hold asks for 1..3 tickets on the nth ACTIVE admission that still has units. The hold takes at most those units.
// Each attempt has its own booking id and buyer, drawn here so the goroutine uses fixed values.
func (d *driftDriver) hold(nth int) func() error {
	d.refs++
	bookingID := newTestUUID()
	ref := fmt.Sprintf("DR%04d", d.refs)
	buyer := []byte(fmt.Sprintf("drift-buyer-%02d", d.rng.Intn(12)))
	want := 1 + d.rng.Intn(3)
	lines := d.lines(3)
	return func() error {
		var admID, visitorID string
		var outstanding int
		var edit bool
		found, err := d.rowAt("booking.admissions", "id::text, visitor_id::text, outstanding_units, cfg_edit_enabled",
			"status='ACTIVE' AND outstanding_units>0", nth, &admID, &visitorID, &outstanding, &edit)
		if err != nil || !found {
			return err
		}
		n := min(want, outstanding)
		_, err = d.uow.Hold(d.ctx, outbound.HoldSpec{
			BookingID: bookingID, PublicRef: ref, EventID: d.eventID, AdmissionID: admID, VisitorID: visitorID,
			BuyerNIKHash: buyer, BuyerEmailEnc: sealedEmail, AmountMinor: int64(10000 * n),
			PaymentExpiresAt: time.Now().Add(10 * time.Minute), Lines: lines[:n], CorrelationID: "drift",
			CfgEditEnabled: edit,
		})
		return ignoreDomain(err)
	}
}

// lines draws k ticket lines: a category and an attendee for each, unique across the event.
func (d *driftDriver) lines(k int) []outbound.HoldLine {
	out := make([]outbound.HoldLine, 0, k)
	for i := 0; i < k; i++ {
		d.holders++
		h := []byte(fmt.Sprintf("drift-holder-%05d", d.holders))
		out = append(out, outbound.HoldLine{
			ItemID: newTestUUID(), CategoryID: d.cats[d.rng.Intn(len(d.cats))], UnitPriceMinor: 10000,
			AttendeeNIKHash: h, AttendeeNIKEnc: h, AttendeeNIKMasked: "****", AttendeeName: "Holder",
		})
	}
	return out
}

// bookingState is what a booking step needs from the booking row, read before the command.
type bookingState struct {
	amount      int64
	expires     time.Time
	version     int
	admissionID string
	buyer       []byte
}

// bookingCall runs fn on the nth pending booking, or does nothing when none is pending.
func (d *driftDriver) bookingCall(nth int, fn func(bookingID string, b bookingState) error) func() error {
	return func() error {
		var id string
		found, err := d.rowAt("booking.bookings", "id::text", "status IN ('PENDING_PAYMENT','VERIFYING')", nth, &id)
		if err != nil || !found {
			return err
		}
		var b bookingState
		err = d.pool.QueryRow(d.ctx, `SELECT amount_minor, payment_expires_at, version, admission_id::text, buyer_nik_hash
			FROM booking.bookings WHERE id=$1::uuid`, id).Scan(&b.amount, &b.expires, &b.version, &b.admissionID, &b.buyer)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		return fn(id, b)
	}
}

// bookingEdit replaces the items of the nth pending booking with 1..3 new tickets, at the booking's current version.
func (d *driftDriver) bookingEdit(nth int) func() error {
	n := 1 + d.rng.Intn(3)
	lines := d.lines(3)
	return d.bookingCall(nth, func(id string, b bookingState) error {
		_, err := d.uow.ReplaceItems(d.ctx, outbound.ReplaceSpec{
			BookingID: id, AdmissionID: b.admissionID, EventID: d.eventID, BuyerNIKHash: b.buyer,
			ExpectedVersion: b.version, NewAmountMinor: int64(10000 * n), Lines: lines[:n], CorrelationID: "drift",
		})
		return ignoreDomain(err)
	})
}

// TestStockDrift_ZeroAfterRandomLifecycles runs seeded random lifecycles on one event with two categories and 40 queue
// entries, through the real unit of work and admission repository, and checks the drift query after every step. A step
// runs one to four operations concurrently. Before the random steps, two fixed checks run: a lapsed UNCLAIMED entry is
// granted again (C6.6), and a batch answers its malformed and repeated entries one by one (C7.2).
func TestStockDrift_ZeroAfterRandomLifecycles(t *testing.T) {
	const (
		entries = 40
		steps   = 150
	)
	pool := testPool(t)
	ctx := context.Background()
	eventID, cats := seedDriftEvent(t, pool, suffixFor(t), 20, 20)
	uow, err := NewUnitOfWork(pool)
	require.NoError(t, err)
	adms := NewAdmissionRepo(pool)
	drift := NewDriftStats(pool)

	d := &driftDriver{ctx: ctx, pool: pool, uow: uow, adms: adms, eventID: eventID, cats: cats, rng: rand.New(rand.NewSource(11))}
	for i := 0; i < entries; i++ {
		d.entries = append(d.entries, driftEntry{
			qid: newTestUUID(), visitor: newTestUUID(), jti: newTestUUID(), seq: int64(i + 1), edit: i%3 != 0,
		})
	}

	// Fixed prelude 1 (C6.6): reserve, let the claim window lapse, reserve again. The second grant is the same admission.
	first := outbound.ReserveEntry{QueueEntryID: d.entries[0].qid, Seq: 1, VisitorID: d.entries[0].visitor, AdmissionJTI: d.entries[0].jti, Units: 2}
	out, err := adms.ReserveBatch(ctx, eventID, []outbound.ReserveEntry{first}, time.Now())
	require.NoError(t, err)
	require.Equal(t, "GRANTED", out[0].Outcome)
	require.NoError(t, adms.ExpireSession(ctx, out[0].AllocationID, time.Now().Add(time.Hour)))
	again, err := adms.ReserveBatch(ctx, eventID, []outbound.ReserveEntry{first}, time.Now())
	require.NoError(t, err)
	assert.Equal(t, "GRANTED", again[0].Outcome, "a lapsed UNCLAIMED entry is granted again")
	assert.Equal(t, out[0].AllocationID, again[0].AllocationID, "the re-grant reuses the lapsed admission")

	// Fixed prelude 2 (C7.2): one batch, three answers: a grant, its repeat, and a malformed visitor.
	second := outbound.ReserveEntry{QueueEntryID: d.entries[1].qid, Seq: 2, VisitorID: d.entries[1].visitor, AdmissionJTI: d.entries[1].jti, Units: 1}
	bad := outbound.ReserveEntry{QueueEntryID: d.entries[2].qid, Seq: 3, VisitorID: "not-a-uuid", AdmissionJTI: d.entries[2].jti, Units: 1}
	per, err := adms.ReserveBatch(ctx, eventID, []outbound.ReserveEntry{second, second, bad}, time.Now())
	require.NoError(t, err)
	assert.Equal(t, "GRANTED", per[0].Outcome)
	assert.Equal(t, per[0], per[1], "a repeated entry gets the outcome of its first occurrence")
	assert.Equal(t, "NO_SUPPLY_NOW", per[2].Outcome, "a malformed entry is answered alone")
	require.Empty(t, mustStockDrift(t, drift, ctx), "drift after the fixed prelude")

	// Fixed preludes 3-5: a hold on a fresh entry, then one terminal path each. Every buyer is its own, so the per-buyer
	// quota does not refuse the holds. The statuses are checked, and the drift after each path.
	holdFresh := func(idx int, seq int64) string {
		e := d.entries[idx]
		_, err := adms.ReserveBatch(ctx, eventID, []outbound.ReserveEntry{{QueueEntryID: e.qid, Seq: seq, VisitorID: e.visitor, AdmissionJTI: e.jti, Units: 2}}, time.Now())
		require.NoError(t, err)
		var admID string
		require.NoError(t, pool.QueryRow(ctx, `SELECT id::text FROM booking.admissions WHERE queue_entry_id=$1::uuid`, e.qid).Scan(&admID))
		_, err = adms.Activate(ctx, admID, time.Now())
		require.NoError(t, err)
		held, err := uow.Hold(ctx, outbound.HoldSpec{
			BookingID: newTestUUID(), PublicRef: fmt.Sprintf("DRPRELUDE%d", idx), EventID: eventID, AdmissionID: admID, VisitorID: e.visitor,
			BuyerNIKHash: []byte(fmt.Sprintf("drift-prelude-%d", idx)), BuyerEmailEnc: sealedEmail, AmountMinor: 20000,
			PaymentExpiresAt: time.Now().Add(10 * time.Minute), Lines: d.lines(2), CorrelationID: "drift",
		})
		require.NoError(t, err)
		return held.BookingID
	}
	statusOf := func(bookingID string) string {
		var status string
		require.NoError(t, pool.QueryRow(ctx, `SELECT status FROM booking.bookings WHERE id=$1::uuid`, bookingID).Scan(&status))
		return status
	}

	// A buyer who never pays: EXPIRED, units back to stock.
	timeout := holdFresh(3, 4)
	acted, err := uow.Expire(ctx, outbound.ExpireSpec{BookingID: timeout, Reason: "PAYMENT_TIMEOUT", CorrelationID: "drift"})
	require.NoError(t, err)
	require.True(t, acted)
	require.Equal(t, "EXPIRED", statusOf(timeout))
	require.Empty(t, mustStockDrift(t, drift, ctx), "drift after the PAYMENT_TIMEOUT prelude")

	// A cancelled booking before payment: CANCELLED, units back to stock.
	cancelled := holdFresh(4, 5)
	acted, err = uow.Expire(ctx, outbound.ExpireSpec{BookingID: cancelled, Reason: "CANCELLED", CorrelationID: "drift"})
	require.NoError(t, err)
	require.True(t, acted)
	require.Equal(t, "CANCELLED", statusOf(cancelled))
	require.Empty(t, mustStockDrift(t, drift, ctx), "drift after the CANCELLED prelude")

	// A payment whose amount differs from the booking's: REVIEW, held stock released (P2-8).
	mismatch := holdFresh(5, 6)
	_, err = uow.Settle(ctx, outbound.SettleSpec{BookingID: mismatch, Method: "VA_BCA", AmountMinor: 21000,
		SettlementTime: time.Now(), CorrelationID: "drift", TicketCodeFn: func(item string) string { return "TK-" + item }})
	require.NoError(t, err)
	require.Equal(t, "REVIEW", statusOf(mismatch))
	require.Empty(t, mustStockDrift(t, drift, ctx), "drift after the amount-mismatch prelude")

	ops := 0
	for step := 0; step < steps; step++ {
		batch := make([]func() error, 0, 4)
		for i := 1 + d.rng.Intn(4); i > 0; i-- {
			batch = append(batch, d.pick())
		}
		ops += len(batch)
		errs := make(chan error, len(batch))
		var wg sync.WaitGroup
		for _, run := range batch {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := run(); err != nil {
					errs <- err
				}
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			require.NoError(t, err, "step %d: a database failure, not a business refusal", step)
		}
		rows, err := drift.StockDrift(ctx)
		require.NoError(t, err)
		require.Empty(t, rows, "step %d (after %d operations): stock drifted", step, ops)
	}

	var statuses string
	require.NoError(t, pool.QueryRow(ctx, `SELECT string_agg(status || '=' || n, ' ' ORDER BY status)
		FROM (SELECT status, count(*) AS n FROM booking.bookings WHERE event_id=$1::uuid GROUP BY status) s`, eventID).Scan(&statuses))
	t.Logf("drift = 0 after %d random steps (%d operations); bookings: %s", steps, ops, statuses)
}

func mustStockDrift(t *testing.T, drift *DriftStats, ctx context.Context) []outbound.StockDrift {
	t.Helper()
	rows, err := drift.StockDrift(ctx)
	require.NoError(t, err)
	return rows
}
