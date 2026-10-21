package entity_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/entity"
	vo "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/valueobject"
)

func TestAdmission_IsActiveAndExpiry(t *testing.T) {
	now := time.Now().UTC()
	a := entity.Admission{Status: vo.AdmissionActive, ExpiresAt: now.Add(time.Minute)}
	assert.True(t, a.IsActive())
	assert.False(t, a.IsExpired(now))
	assert.True(t, a.IsExpired(now.Add(2*time.Minute)))

	ended := entity.Admission{Status: vo.AdmissionExpired}
	assert.False(t, ended.IsActive())
}

func TestAdmission_EditRules(t *testing.T) {
	a := entity.Admission{
		Status: vo.AdmissionActive, ExpiresAt: time.Now().Add(time.Hour),
		CfgEditEnabled: true, CfgMaxEdits: 3, EditCount: 1,
	}
	assert.Equal(t, 2, a.EditsRemaining())
	assert.True(t, a.CanEdit())

	a.EditCount = 3
	assert.Equal(t, 0, a.EditsRemaining())
	assert.False(t, a.CanEdit())

	a.EditCount = 0
	a.CfgEditEnabled = false
	assert.False(t, a.CanEdit())
}

func TestAdmission_ChecksRemaining(t *testing.T) {
	a := entity.Admission{AttendeeChecks: 8}
	assert.Equal(t, 2, a.ChecksRemaining(10))
	assert.Equal(t, 0, a.ChecksRemaining(5))
}

func TestBooking_LockAndEdit(t *testing.T) {
	b := entity.Booking{Status: vo.BookingPendingPayment}
	assert.True(t, b.CanEdit())
	assert.False(t, b.IsLocked())

	started := time.Now()
	b.PaymentStartedAt = &started
	assert.True(t, b.IsLocked())
	assert.False(t, b.CanEdit())
}

func TestInventoryAndSupply(t *testing.T) {
	inv := entity.Inventory{Capacity: 50, HeldQuantity: 30, SoldQuantity: 15}
	assert.Equal(t, 5, inv.Available())
	inv.SoldQuantity = 20
	assert.Equal(t, 0, inv.Available())

	s := entity.Supply{TotalCapacity: 100, AllocatedUnits: 40, HeldUnits: 30, SoldUnits: 30}
	assert.Equal(t, 0, s.Free())
	assert.True(t, s.SoldOut())
}
