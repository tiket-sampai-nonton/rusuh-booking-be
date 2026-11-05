//go:build integration

package postgres

import (
	"context"
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
