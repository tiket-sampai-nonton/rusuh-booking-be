package valueobject_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	vo "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/valueobject"
)

func TestBookingStatus_Transitions(t *testing.T) {
	assert.True(t, vo.BookingPendingPayment.CanTransitionTo(vo.BookingVerifying))
	assert.True(t, vo.BookingPendingPayment.CanTransitionTo(vo.BookingExpired))
	assert.True(t, vo.BookingVerifying.CanTransitionTo(vo.BookingPaid))
	assert.True(t, vo.BookingVerifying.CanTransitionTo(vo.BookingReview))
	assert.False(t, vo.BookingPaid.CanTransitionTo(vo.BookingExpired))
	assert.False(t, vo.BookingExpired.CanTransitionTo(vo.BookingPaid))
}

func TestBookingStatus_Predicates(t *testing.T) {
	assert.True(t, vo.BookingPendingPayment.IsPayable())
	assert.True(t, vo.BookingVerifying.IsPayable())
	assert.False(t, vo.BookingPaid.IsPayable())
	assert.True(t, vo.BookingPaid.IsTerminal())
	assert.False(t, vo.BookingVerifying.IsTerminal())
	assert.True(t, vo.BookingStatus("PAID").Valid())
	assert.False(t, vo.BookingStatus("NOPE").Valid())
}

func TestAdmissionAndItemStatus(t *testing.T) {
	assert.True(t, vo.AdmissionReserved.IsActive())
	assert.True(t, vo.AdmissionEditable.IsActive())
	assert.False(t, vo.AdmissionExpired.IsActive())
	assert.True(t, vo.ItemHeld.IsActive())
	assert.True(t, vo.ItemPaid.IsActive())
	assert.False(t, vo.ItemReplaced.IsActive())
}

func TestMoney(t *testing.T) {
	m, err := vo.NewMoney(1000)
	require.NoError(t, err)
	assert.EqualValues(t, 1000, m.Minor())
	assert.EqualValues(t, 3000, m.Mul(3).Minor())
	assert.EqualValues(t, 1500, m.Add(mustMoney(t, 500)).Minor())

	_, err = vo.NewMoney(-1)
	assert.ErrorIs(t, err, vo.ErrNegativeMoney)
}

func TestVersion(t *testing.T) {
	v := vo.Version(1)
	assert.Equal(t, vo.Version(2), v.Next())
	assert.True(t, v.Matches(1))
	assert.False(t, v.Matches(2))
}

func mustMoney(t *testing.T, n int64) vo.Money {
	t.Helper()
	m, err := vo.NewMoney(n)
	require.NoError(t, err)
	return m
}
