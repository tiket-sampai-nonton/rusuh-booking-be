//go:build integration

package postgres

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/apperror"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

func (f acctFixture) seedQuota(t *testing.T, hash []byte, held, paid int) {
	t.Helper()
	_, err := f.pool.Exec(context.Background(), `
		INSERT INTO booking.buyer_quotas (event_id, buyer_nik_hash, held_quantity, paid_quantity, max_quantity)
		VALUES ($1::uuid, $2, $3, $4, 3)`, f.eventID, hash, held, paid)
	require.NoError(t, err)
}

func (f acctFixture) admission(t *testing.T, id string) (status string, outstanding, refusals int, buyerHash []byte) {
	t.Helper()
	require.NoError(t, f.pool.QueryRow(context.Background(),
		`SELECT status, outstanding_units, buyer_nik_refusals, buyer_nik_hash FROM booking.admissions WHERE id=$1::uuid`, id).
		Scan(&status, &outstanding, &refusals, &buyerHash))
	return
}

func (f acctFixture) quotaRows(t *testing.T) int {
	t.Helper()
	var n int
	require.NoError(t, f.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM booking.buyer_quotas WHERE event_id=$1::uuid`, f.eventID).Scan(&n))
	return n
}

// TC-ANON-11: a buyer NIK that already has k tickets may choose at most 3-k.
func TestHoldBuyerNIK_RemainingCapTable(t *testing.T) {
	for k := 0; k <= 3; k++ {
		for n := 1; n <= 3; n++ {
			k, n := k, n
			t.Run(fmt.Sprintf("k=%d,n=%d", k, n), func(t *testing.T) {
				f := newAcctFixture(t, suffixFor(t), 9)
				adm, vis := f.reserve(t, 1, 3, true)
				if k > 0 {
					f.seedQuota(t, []byte(fmt.Sprintf("buyer%08d", 1)), k/2, k-k/2)
				}
				res, err := f.uow.Hold(context.Background(), f.spec(1, n, adm, vis, true))
				require.NoError(t, err, "a refusal is a committed result, not an error")
				status, outstanding, refusals, _ := f.admission(t, adm)
				if k+n <= 3 {
					assert.Empty(t, res.Refused)
					assert.Equal(t, "EDITABLE", status)
					assert.Equal(t, 0, outstanding)
					return
				}
				assert.Equal(t, outbound.RefusedBuyerNikLimit, res.Refused)
				assert.Equal(t, "ACTIVE", status, "admission stays ACTIVE")
				assert.Equal(t, 3, outstanding, "the buyer keeps all reserved units")
				assert.Equal(t, 1, refusals, "the attempt counter survives the refusal")
				l := f.ledger(t)
				assert.Equal(t, 3, l.alloc)
				assert.Equal(t, 0, l.held)
			})
		}
	}
}

func TestHoldBuyerNIK_RetryWithAnotherBuyerNIKAfterRefusal(t *testing.T) {
	f := newAcctFixture(t, suffixFor(t), 9)
	adm, vis := f.reserve(t, 1, 3, true)
	f.seedQuota(t, []byte(fmt.Sprintf("buyer%08d", 1)), 0, 3)
	res, err := f.uow.Hold(context.Background(), f.spec(1, 2, adm, vis, true))
	require.NoError(t, err)
	require.Equal(t, outbound.RefusedBuyerNikLimit, res.Refused)

	retry := f.spec(1, 2, adm, vis, true)
	retry.BuyerNIKHash = []byte("anotherbuyer0001")
	res, err = f.uow.Hold(context.Background(), retry)
	require.NoError(t, err)
	assert.Empty(t, res.Refused)
	status, outstanding, _, bound := f.admission(t, adm)
	assert.Equal(t, "EDITABLE", status)
	assert.Equal(t, 0, outstanding)
	assert.Equal(t, []byte("anotherbuyer0001"), bound, "the buyer NIK is bound at hold")
	l := f.ledger(t)
	assert.Equal(t, 7, l.total-l.alloc-l.held-l.sold, "2 held, the unused unit is back in free stock")
}

// TC-ANON-12: two admissions may bind the same buyer NIK; the shared cap is enforced at hold, never as a
// unique violation / 500.
func TestHoldBuyerNIK_SameBuyerNIKInTwoAdmissions(t *testing.T) {
	const pairs = 200
	f := newAcctFixture(t, suffixFor(t), pairs*6)
	var wg sync.WaitGroup
	type pairResult struct {
		res  [2]outbound.HoldResult
		errs [2]error
		hash []byte
	}
	out := make([]pairResult, pairs)
	for p := 0; p < pairs; p++ {
		a1, v1 := f.reserve(t, 2*p, 3, true)
		a2, v2 := f.reserve(t, 2*p+1, 3, true)
		same := []byte(fmt.Sprintf("shared-buyer-%04d", p))
		out[p].hash = same
		s1 := f.spec(2*p, 2, a1, v1, true)
		s2 := f.spec(2*p+1, 2, a2, v2, true)
		s1.BuyerNIKHash, s2.BuyerNIKHash = same, same
		for i, s := range []outbound.HoldSpec{s1, s2} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				out[p].res[i], out[p].errs[i] = f.uow.Hold(context.Background(), s)
			}()
		}
	}
	wg.Wait()
	for p, r := range out {
		require.NoError(t, r.errs[0], "pair %d", p)
		require.NoError(t, r.errs[1], "pair %d: never a unique-violation/500", p)
		won := 0
		for _, x := range r.res {
			if x.Refused == "" {
				won++
			} else {
				assert.Equal(t, outbound.RefusedBuyerNikLimit, x.Refused)
			}
		}
		assert.Equal(t, 1, won, "pair %d: 2+2 > 3 so exactly one admission wins", p)
	}
}

// Precedence (D§5.3): a sold-out category wins over the NIK cap and leaves everything untouched,
// including the attempt counter and the buyer_quotas probe row.
func TestHoldBuyerNIK_CategorySoldOutWinsAndRollsBack(t *testing.T) {
	f := newAcctFixture(t, suffixFor(t), 6)
	adm, vis := f.reserve(t, 1, 3, true)
	f.seedQuota(t, []byte(fmt.Sprintf("buyer%08d", 1)), 0, 3) // at cap
	_, err := f.pool.Exec(context.Background(),
		`UPDATE booking.category_inventories SET held_quantity=capacity WHERE category_id=$1::uuid`, f.catID)
	require.NoError(t, err)
	before := f.quotaRows(t)

	_, err = f.uow.Hold(context.Background(), f.spec(1, 1, adm, vis, true))
	require.Error(t, err)
	var ae *apperror.Error
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, inbound.CodeCategoryUnavailable, ae.Code, "typed category refusal")
	assert.Contains(t, err.Error(), f.catID, "refused category ids are listed in the message")
	status, outstanding, refusals, _ := f.admission(t, adm)
	assert.Equal(t, "ACTIVE", status)
	assert.Equal(t, 3, outstanding, "the event-level units stay")
	assert.Equal(t, 0, refusals)
	assert.Equal(t, before, f.quotaRows(t))
}

func TestHoldBuyerNIK_UnseenNikProbeRowRolledBackOnCategoryAndThrottle(t *testing.T) {
	f := newAcctFixture(t, suffixFor(t), 6)
	adm, vis := f.reserve(t, 1, 3, true)
	_, err := f.pool.Exec(context.Background(),
		`UPDATE booking.category_inventories SET held_quantity=capacity WHERE category_id=$1::uuid`, f.catID)
	require.NoError(t, err)
	_, err = f.uow.Hold(context.Background(), f.spec(1, 1, adm, vis, true))
	require.Error(t, err)
	assert.Equal(t, 0, f.quotaRows(t), "category refusal leaves no buyer_quotas row")
}

// TC-ANON-13: attempt budget then capped exponential throttle on the injected clock.
func TestHoldThrottle_BudgetThenBackoff(t *testing.T) {
	f := newAcctFixture(t, suffixFor(t), 9)
	adm, vis := f.reserve(t, 1, 3, true)
	f.seedQuota(t, []byte(fmt.Sprintf("buyer%08d", 1)), 0, 3)
	t0 := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)
	// The hold reads its clock from the spec (C3.1), so the admission lives on the same injected timeline.
	_, err := f.pool.Exec(context.Background(), `UPDATE booking.admissions SET expires_at=$2 WHERE id=$1::uuid`, adm, t0.Add(10*time.Minute))
	require.NoError(t, err)
	mk := func(at time.Time) outbound.HoldSpec {
		s := f.spec(1, 1, adm, vis, true)
		s.Now, s.RefusalBudget, s.ThrottleBase, s.ThrottleMax = at, 2, 2*time.Second, 5*time.Second
		return s
	}
	for i := 0; i < 2; i++ { // within budget: plain refusals
		res, err := f.uow.Hold(context.Background(), mk(t0))
		require.NoError(t, err)
		require.Equal(t, outbound.RefusedBuyerNikLimit, res.Refused)
	}
	// budget used: the next attempt inside base window is throttled with a retry-after >= 1s
	_, err = f.uow.Hold(context.Background(), mk(t0.Add(time.Second)))
	require.Error(t, err)
	var ae *apperror.Error
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, inbound.CodeBuyerNikThrottled, ae.Code)
	assert.NotEmpty(t, ae.Details["retryAfter"])
	_, _, refusals, _ := f.admission(t, adm)
	assert.Equal(t, 2, refusals, "a throttled attempt does not count and writes nothing")

	// after the window the attempt is evaluated again (and refused: still at cap), counter grows
	res, err := f.uow.Hold(context.Background(), mk(t0.Add(3*time.Second)))
	require.NoError(t, err)
	assert.Equal(t, outbound.RefusedBuyerNikLimit, res.Refused)
	// the next window doubles (4s) and is capped by max (5s)
	_, err = f.uow.Hold(context.Background(), mk(t0.Add(3*time.Second+3*time.Second)))
	require.Error(t, err, "second step is 4s")
	res, err = f.uow.Hold(context.Background(), mk(t0.Add(3*time.Second+5*time.Second)))
	require.NoError(t, err, "capped at 5s")
	assert.Equal(t, outbound.RefusedBuyerNikLimit, res.Refused)
}

// Holder NIK uniqueness per event (invariant: 1 ticket per holder NIK) still holds with a shared buyer.
func TestHoldBuyerNIK_HolderNIKUniquePerEvent(t *testing.T) {
	f := newAcctFixture(t, suffixFor(t), 9)
	a1, v1 := f.reserve(t, 1, 3, true)
	a2, v2 := f.reserve(t, 2, 3, true)
	s1 := f.spec(1, 1, a1, v1, true)
	s2 := f.spec(2, 1, a2, v2, true)
	s2.Lines[0].AttendeeNIKHash = s1.Lines[0].AttendeeNIKHash // same holder NIK
	_, err := f.uow.Hold(context.Background(), s1)
	require.NoError(t, err)
	_, err = f.uow.Hold(context.Background(), s2)
	assert.ErrorIs(t, err, inbound.ErrNikRegistered)
	status, outstanding, _, _ := f.admission(t, a2)
	assert.Equal(t, "ACTIVE", status)
	assert.Equal(t, 3, outstanding, "failed hold keeps the reserved units")
}

// No oversell with 3-unit admissions under concurrency, and every unit is accounted for.
func TestHoldBuyerNIK_NoOversellUnderConcurrency(t *testing.T) {
	const adms = 12
	f := newAcctFixture(t, suffixFor(t), 20) // 12 admissions x 3 would need 36 > 20: supply admits only some
	var reserved int32
	type j struct{ adm, vis string }
	var jobs []j
	free := 20
	for i := 0; i < adms; i++ {
		u := 3
		if free < u {
			u = free
		}
		if u == 0 {
			break
		}
		free -= u
		a, v := f.reserve(t, i, u, true)
		jobs = append(jobs, j{a, v})
		atomic.AddInt32(&reserved, int32(u))
	}
	var wg sync.WaitGroup
	var held int32
	for i, jb := range jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := f.uow.Hold(context.Background(), f.spec(i, 1, jb.adm, jb.vis, i%2 == 0))
			if err == nil && res.Refused == "" {
				atomic.AddInt32(&held, 1)
			}
		}()
	}
	wg.Wait()
	l := f.ledger(t)
	assert.Equal(t, int(held), l.held)
	assert.Equal(t, 0, l.alloc)
	assert.LessOrEqual(t, l.alloc+l.held+l.sold, l.total, "never oversell")
}
