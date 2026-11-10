//go:build integration

package postgres

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

// TC-ANON-18 (branch E1): after hold the admission has no reserved units left, so an edit that adds
// tickets takes them from free stock (refused when none) and a decrease returns them immediately.
func (f acctFixture) holdOne(t *testing.T, idx, units, n int) (spec outbound.HoldSpec) {
	t.Helper()
	adm, vis := f.reserve(t, idx, units, true)
	spec = f.spec(idx, n, adm, vis, true)
	_, err := f.uow.Hold(context.Background(), spec)
	require.NoError(t, err)
	return spec
}

func (f acctFixture) replaceSpec(spec outbound.HoldSpec, n int, version int) outbound.ReplaceSpec {
	lines := f.spec(900, n, spec.AdmissionID, spec.VisitorID, true).Lines
	return outbound.ReplaceSpec{
		BookingID: spec.BookingID, AdmissionID: spec.AdmissionID, EventID: f.eventID,
		BuyerNIKHash: spec.BuyerNIKHash, ExpectedVersion: version,
		NewAmountMinor: int64(10000 * n), Lines: lines, CorrelationID: "c",
	}
}

func TestEditE1_IncreaseNeedsFreeStock(t *testing.T) {
	f := newAcctFixture(t, suffixFor(t), 3)
	spec := f.holdOne(t, 1, 3, 1)   // capacity 3, holds 1, 2 free
	_, _ = f.reserve(t, 2, 2, true) // another buyer reserves the remaining 2 → free 0

	_, err := f.uow.ReplaceItems(context.Background(), f.replaceSpec(spec, 2, 1))
	require.Error(t, err)
	assert.ErrorIs(t, err, inbound.ErrCategoryUnavailable)
	l := f.ledger(t)
	assert.Equal(t, 1, l.held, "previous order unchanged")
	assert.Equal(t, 2, l.alloc, "other buyer's reservation untouched")
}

func TestEditE1_IncreaseUsesFreeStockAndDecreaseReturnsIt(t *testing.T) {
	f := newAcctFixture(t, suffixFor(t), 6)
	spec := f.holdOne(t, 1, 3, 1)

	v, err := f.uow.ReplaceItems(context.Background(), f.replaceSpec(spec, 3, 1))
	require.NoError(t, err)
	l := f.ledger(t)
	assert.Equal(t, 3, l.held)
	assert.Equal(t, 0, l.alloc, "increase never touches other reservations")
	assert.Equal(t, 3, l.total-l.alloc-l.held-l.sold)

	_, err = f.uow.ReplaceItems(context.Background(), f.replaceSpec(spec, 1, v))
	require.NoError(t, err)
	l = f.ledger(t)
	assert.Equal(t, 1, l.held)
	assert.Equal(t, 5, l.total-l.alloc-l.held-l.sold, "decrease returns units to free stock immediately")
}

// activeHashes lists the NIK hashes of the HELD/PAID items of a booking, sorted by hash.
func (f acctFixture) activeHashes(t *testing.T, bookingID string) []string {
	t.Helper()
	rows, err := f.pool.Query(context.Background(),
		`SELECT attendee_nik_hash FROM booking.booking_items
		 WHERE booking_id=$1::uuid AND item_status IN ('HELD','PAID') ORDER BY attendee_nik_hash`, bookingID)
	require.NoError(t, err)
	defer rows.Close()
	var out []string
	for rows.Next() {
		var h []byte
		require.NoError(t, rows.Scan(&h))
		out = append(out, string(h))
	}
	require.NoError(t, rows.Err())
	return out
}

// keepSpec builds a replace spec that re-inserts the holders in `keep` (same hash/enc/masked/name, as the
// Edit usecase does for keepFromItemId) plus `fresh` new holders.
func (f acctFixture) keepSpec(spec outbound.HoldSpec, keep []outbound.HoldLine, fresh, version int, tag string) outbound.ReplaceSpec {
	lines := append([]outbound.HoldLine{}, keep...)
	for i := 0; i < fresh; i++ {
		h := []byte(fmt.Sprintf("fresh%s%04d", tag, i))
		lines = append(lines, outbound.HoldLine{CategoryID: f.catID, UnitPriceMinor: 10000,
			AttendeeNIKHash: h, AttendeeNIKEnc: h, AttendeeNIKMasked: "****", AttendeeName: "Fresh"})
	}
	return outbound.ReplaceSpec{
		BookingID: spec.BookingID, AdmissionID: spec.AdmissionID, EventID: f.eventID,
		BuyerNIKHash: spec.BuyerNIKHash, ExpectedVersion: version,
		NewAmountMinor: int64(10000 * len(lines)), Lines: lines, CorrelationID: "c",
	}
}

// TC-EDT-07 (P-1): keeping a holder re-inserts the same NIK hash once; counters equal the new line count and no
// unique index (uq_attendee_nik_per_event, uq_booking_item_nik_active) is violated.
func TestEdit_Keep_CountersAndUniqueness(t *testing.T) {
	f := newAcctFixture(t, suffixFor(t), 6)
	spec := f.holdOne(t, 1, 3, 2)

	_, err := f.uow.ReplaceItems(context.Background(), f.keepSpec(spec, spec.Lines[:1], 1, 1, "a"))
	require.NoError(t, err)

	l := f.ledger(t)
	assert.Equal(t, 2, l.held, "kept + fresh counted once each")
	assert.Equal(t, 4, l.total-l.alloc-l.held-l.sold)
	hashes := f.activeHashes(t, spec.BookingID)
	require.Len(t, hashes, 2)
	assert.Contains(t, hashes, string(spec.Lines[0].AttendeeNIKHash), "kept holder is still active exactly once")
	assert.NotContains(t, hashes, string(spec.Lines[1].AttendeeNIKHash), "dropped holder released")
}

// P-6: two edits racing on the same version, one of them keeping a holder: exactly one commits, the other gets a
// version conflict, and the ledger equals the committed order.
func TestEdit_Keep_ConcurrentEditsSingleWinner(t *testing.T) {
	f := newAcctFixture(t, suffixFor(t), 6)
	spec := f.holdOne(t, 1, 3, 2)

	specs := []outbound.ReplaceSpec{
		f.keepSpec(spec, spec.Lines[:1], 1, 1, "x"), // keep 1 + 1 fresh = 2
		f.keepSpec(spec, spec.Lines[:2], 1, 1, "y"), // keep 2 + 1 fresh = 3
	}
	errs := make([]error, len(specs))
	var wg sync.WaitGroup
	for i := range specs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = f.uow.ReplaceItems(context.Background(), specs[i])
		}(i)
	}
	wg.Wait()

	wins, winner := 0, -1
	for i, e := range errs {
		if e == nil {
			wins++
			winner = i
			continue
		}
		assert.ErrorIs(t, e, inbound.ErrVersionConflict)
	}
	require.Equal(t, 1, wins, "exactly one edit commits: %v", errs)
	assert.Equal(t, len(specs[winner].Lines), f.ledger(t).held)
	assert.Len(t, f.activeHashes(t, spec.BookingID), len(specs[winner].Lines))
}
