//go:build integration

package postgres

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

// TC-ANON-14: the hold must consume only the admission's own reserved units and give the unused
// units back to free stock at hold (owner decision 5). These tests are written against the
// HoldSpec/uow.Hold API and are the regression for the "stealing" defects F1/F2.

type acctFixture struct {
	pool    *pgxpool.Pool
	uow     *UnitOfWork
	eventID string
	catID   string
}

func newAcctFixture(t *testing.T, suffix string, capacity int) acctFixture {
	t.Helper()
	pool := testPool(t)
	eventID, catID := seedTestEvent(t, pool, suffix, capacity)
	// seedTestEvent marks everything as allocated; the accounting tests set it explicitly.
	_, err := pool.Exec(context.Background(),
		`UPDATE booking.event_supply SET allocated_units=0 WHERE event_id=$1::uuid`, eventID)
	require.NoError(t, err)
	uow, err := NewUnitOfWork(pool)
	require.NoError(t, err)
	return acctFixture{pool: pool, uow: uow, eventID: eventID, catID: catID}
}

// reserve inserts an ACTIVE admission holding `units` reserved units (counted in allocated_units).
func (f acctFixture) reserve(t *testing.T, idx, units int, editEnabled bool) (admID, visitorID string) {
	t.Helper()
	ctx := context.Background()
	admID = fmt.Sprintf("ad000000-0000-0000-%04d-%s", idx, f.eventID[len(f.eventID)-12:])
	visitorID = fmt.Sprintf("b1500000-0000-0000-%04d-%s", idx, f.eventID[len(f.eventID)-12:])
	_, err := f.pool.Exec(ctx, `
		INSERT INTO booking.admissions
			(id, event_id, queue_entry_id, queue_seq, visitor_id, buyer_nik_hash, buyer_email_enc,
			 units, outstanding_units, status, admission_jti,
			 cfg_selection_ttl_seconds, cfg_payment_ttl_seconds, cfg_submit_grace_seconds,
			 cfg_edit_enabled, cfg_max_edits, cfg_allow_mixed_categories, reserved_at, expires_at)
		VALUES ($1::uuid, $2::uuid, gen_random_uuid(), $3, $4::uuid, $5, 'x',
			$6, $6, 'ACTIVE', gen_random_uuid(), 300, 600, 5, $7, 3, true, now(), now()+interval '10 minutes')`,
		admID, f.eventID, idx+1, visitorID, []byte(fmt.Sprintf("buyer%08d", idx)), units, editEnabled)
	require.NoError(t, err)
	_, err = f.pool.Exec(ctx,
		`UPDATE booking.event_supply SET allocated_units=allocated_units+$2 WHERE event_id=$1::uuid`, f.eventID, units)
	require.NoError(t, err)
	return admID, visitorID
}

func (f acctFixture) spec(idx, n int, admID, visitorID string, editEnabled bool) outbound.HoldSpec {
	lines := make([]outbound.HoldLine, 0, n)
	for i := 0; i < n; i++ {
		h := []byte(fmt.Sprintf("holder%04d%04d", idx, i))
		lines = append(lines, outbound.HoldLine{
			ItemID: newTestUUID(), CategoryID: f.catID, UnitPriceMinor: 10000,
			AttendeeNIKHash: h, AttendeeNIKEnc: h, AttendeeNIKMasked: "****", AttendeeName: "Holder",
		})
	}
	hash := []byte(fmt.Sprintf("buyer%08d", idx))
	return outbound.HoldSpec{
		BookingID: fmt.Sprintf("b0000000-0000-0000-%04d-%s", idx, f.eventID[len(f.eventID)-12:]),
		// Deterministic and unique: the event id suffix is unique per test run, idx per hold. (A clock-derived ref could
		// collide between concurrent holds and flake the oracle.)
		PublicRef:        fmt.Sprintf("AC%s%04d", f.eventID[len(f.eventID)-12:], idx),
		EventID:          f.eventID,
		AdmissionID:      admID,
		VisitorID:        visitorID,
		BuyerNIKHash:     hash,
		BuyerEmailEnc:    sealedEmail,
		AmountMinor:      int64(10000 * n),
		PaymentExpiresAt: time.Now().Add(10 * time.Minute),
		Lines:            lines,
		CorrelationID:    "corr",
		CfgEditEnabled:   editEnabled,
	}
}

type ledger struct{ total, alloc, held, sold int }

func (f acctFixture) ledger(t *testing.T) ledger {
	t.Helper()
	var l ledger
	require.NoError(t, f.pool.QueryRow(context.Background(),
		`SELECT total_capacity, allocated_units, held_units, sold_units FROM booking.event_supply WHERE event_id=$1::uuid`,
		f.eventID).Scan(&l.total, &l.alloc, &l.held, &l.sold))
	return l
}

func suffixFor(t *testing.T) string {
	return fmt.Sprintf("%012d", time.Now().UnixNano()%1e12)
}

func TestHoldAccounting_UnusedUnitsReturnAtHold(t *testing.T) {
	for _, edit := range []bool{true, false} {
		edit := edit
		t.Run(fmt.Sprintf("edit=%v", edit), func(t *testing.T) {
			f := newAcctFixture(t, suffixFor(t), 6)
			aID, aV := f.reserve(t, 1, 3, edit)
			_, _ = f.reserve(t, 2, 3, edit)

			_, err := f.uow.Hold(context.Background(), f.spec(1, 1, aID, aV, edit))
			require.NoError(t, err, "A must be able to hold 1 of its 3 reserved units")

			l := f.ledger(t)
			assert.Equal(t, 3, l.alloc, "only B's 3 units stay reserved; A's unused 2 return to stock")
			assert.Equal(t, 1, l.held)
			assert.Equal(t, 2, l.total-l.alloc-l.held-l.sold, "free stock = 2")
		})
	}
}

func TestHoldAccounting_CannotStealOtherReservations(t *testing.T) {
	// Stock 7 reserved as 3,3,1. The admission with 1 unit tries to hold 3.
	f := newAcctFixture(t, suffixFor(t), 7)
	_, _ = f.reserve(t, 1, 3, true)
	_, _ = f.reserve(t, 2, 3, true)
	cID, cV := f.reserve(t, 3, 1, true)

	_, err := f.uow.Hold(context.Background(), f.spec(3, 3, cID, cV, true))
	require.Error(t, err, "holding more than the admission's own units must be refused")

	l := f.ledger(t)
	assert.Equal(t, 7, l.alloc, "others' reservations are untouched")
	assert.Equal(t, 0, l.held)
}

// P1/P2/P3 (R09.3): 20 admissions x 3 units on capacity 60, seeded n, concurrent; exact oracle.
func TestHoldAccounting_ConcurrentExactOracle(t *testing.T) {
	const adms = 20
	f := newAcctFixture(t, suffixFor(t), 60)
	rng := rand.New(rand.NewSource(42))
	type job struct {
		idx, n    int
		adm, vis  string
		editFlag  bool
		duplicate bool
	}
	jobs := make([]job, 0, adms)
	sum := 0
	for i := 0; i < adms; i++ {
		edit := i%2 == 0
		a, v := f.reserve(t, i, 3, edit)
		n := 1 + rng.Intn(3)
		sum += n
		jobs = append(jobs, job{idx: i, n: n, adm: a, vis: v, editFlag: edit})
	}
	var wg sync.WaitGroup
	errs := make([]error, adms)
	for i, j := range jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = f.uow.Hold(context.Background(), f.spec(j.idx, j.n, j.adm, j.vis, j.editFlag))
		}()
	}
	wg.Wait()
	for i, e := range errs {
		require.NoError(t, e, "hold %d", i)
	}
	l := f.ledger(t)
	assert.Equal(t, 0, l.alloc, "every admission released its unused units; none reserved remain")
	assert.Equal(t, sum, l.held)
	assert.Equal(t, 60-sum, l.total-l.alloc-l.held-l.sold)
}
